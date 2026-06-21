// Package snapshot reads and writes evals: a single markdown file with YAML
// frontmatter (the machine-readable metadata) and a body holding the user
// prompts. One file == one eval. A benchy run is a set of these.
package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/option-ai/benchy/cli/internal/config"
	"gopkg.in/yaml.v3"
)

// Gates are the deterministic shell commands run against an agent's resulting
// worktree. Any may be empty (skipped). They are auto-detected by the skill and
// hand-editable afterwards.
type Gates struct {
	Build string `yaml:"build,omitempty"`
	Test  string `yaml:"test,omitempty"`
	Lint  string `yaml:"lint,omitempty"`
}

// meta is the YAML frontmatter block.
type meta struct {
	Title  string `yaml:"title"`
	Repo   string `yaml:"repo,omitempty"`   // optional: empty => scratch eval
	Commit string `yaml:"commit,omitempty"` // optional: required only if Repo is set
	// SourcePath is the absolute path of the repo on the capturing machine —
	// the clone fallback when the remote needs auth this process doesn't have.
	SourcePath string            `yaml:"source_path,omitempty"`
	Created    string            `yaml:"created"`
	Feedback   string            `yaml:"feedback,omitempty"`
	Replay     config.ReplayMode `yaml:"replay,omitempty"`
	// Expects declares the deliverable the judge should grade:
	// "diff", "answer", "conversation", or "" (auto — judge infers from the
	// task). "conversation" implies sequential replay.
	Expects string `yaml:"expects,omitempty"`
	Gates   Gates  `yaml:"gates,omitempty"`
	// Source records how the eval was captured: "auto" for hook-driven
	// prospective capture, empty for the manual /add-to-benchy skill.
	Source string `yaml:"source,omitempty"`
	// Images are the filenames of reference images stored in the eval's sibling
	// assets dir (<eval>.assets/). Captured from a session's pasted screenshots
	// and passed to the agent at replay so image-driven tasks are reproducible.
	Images []string `yaml:"images,omitempty"`
}

// Snapshot is a parsed eval. Path is set on load and not serialized.
type Snapshot struct {
	meta    `yaml:",inline"`
	Prompts []string `yaml:"-"`
	Path    string   `yaml:"-"`
	// Notes is an optional comment block rendered between the frontmatter and
	// the prompts — used by auto-capture to record the filter verdict for
	// review. It is not parsed back on load (capture-time only).
	Notes string `yaml:"-"`
}

// promptDelim separates prompts in the body. Rendered invisibly in markdown.
const promptDelim = "<!-- prompt -->"

var frontmatterRe = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n?(.*)$`)

// Parse decodes a snapshot from raw markdown bytes.
func Parse(raw []byte) (*Snapshot, error) {
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("missing YAML frontmatter")
	}
	var s Snapshot
	if err := yaml.Unmarshal(m[1], &s.meta); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	s.Prompts = parsePrompts(string(m[2]))
	// Replay is left empty when unset; the runner falls back to the config
	// default (and ultimately oneshot).
	return &s, nil
}

// parsePrompts pulls each delimited block out of the body, trimming the
// "## Prompts" heading and surrounding whitespace.
func parsePrompts(body string) []string {
	idx := strings.Index(body, promptDelim)
	if idx < 0 {
		return nil
	}
	parts := strings.Split(body[idx:], promptDelim)
	var out []string
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Load reads and parses a snapshot file.
func Load(path string) (*Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	s.Path = path
	return s, nil
}

// LoadAll reads every *.md snapshot in the snapshots dir, sorted by title.
func LoadAll() ([]*Snapshot, error) {
	dir := config.SnapshotsDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Snapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		s, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

// Render serializes a snapshot back to markdown bytes.
func (s *Snapshot) Render() ([]byte, error) {
	fm, err := yaml.Marshal(s.meta)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n\n")
	if n := strings.TrimSpace(s.Notes); n != "" {
		b.WriteString(n)
		b.WriteString("\n\n")
	}
	b.WriteString("## Prompts\n\n")
	for _, p := range s.Prompts {
		b.WriteString(promptDelim)
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(p))
		b.WriteString("\n\n")
	}
	return []byte(b.String()), nil
}

// Save writes the snapshot to the snapshots dir under <title>.md.
func (s *Snapshot) Save() error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}
	if s.Path == "" {
		s.Path = filepath.Join(config.SnapshotsDir(), Slug(s.Title)+".md")
	}
	b, err := s.Render()
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, b, 0o644)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a title into a filesystem-safe kebab-case identifier.
func Slug(title string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(title), "-")
	return strings.Trim(s, "-")
}

// AssetsDir is the sibling directory holding this eval's reference images:
// for <name>.md it is <name>.assets. Empty when the eval has no path yet.
func (s *Snapshot) AssetsDir() string {
	if s.Path == "" {
		return ""
	}
	return strings.TrimSuffix(s.Path, ".md") + ".assets"
}

// ImagePaths returns the absolute paths of the eval's reference images that
// actually exist on disk, in declared order.
func (s *Snapshot) ImagePaths() []string {
	dir := s.AssetsDir()
	if dir == "" {
		return nil
	}
	var out []string
	for _, name := range s.Images {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// IsScratch reports whether this eval has no repo anchor. Scratch evals run in
// a fresh empty workspace — for sessions captured outside a git repo (Claude
// Desktop, ChatGPT desktop, Cowork, etc.) or for from-scratch tasks.
func (s *Snapshot) IsScratch() bool { return strings.TrimSpace(s.Repo) == "" }
