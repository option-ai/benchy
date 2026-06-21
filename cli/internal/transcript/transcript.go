// Package transcript parses Claude Code session logs (the JSONL files under
// ~/.claude/projects/<slug>/*.jsonl) into a structured view, and judges whether
// a session is portable enough to become a benchy eval.
//
// It exists for prospective capture: a Stop/SessionEnd hook hands us a finished
// session's transcript, we extract the user's task prompts and the signals that
// predict a bad eval (MCP/live-data dependence, slash-command/skill steering,
// multi-task sprawl, screenshots, pure conversational ops), and only portable
// sessions are written out as candidates.
package transcript

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
)

// Session is the distilled view of one Claude Code session.
type Session struct {
	ID       string
	Title    string   // from the session's ai-title entry, if present
	Cwd      string   // last cwd seen
	Branches []string // distinct git branches touched (sorted, deduped)
	Prompts  []string // genuine free-text user prompts, harness noise removed

	Commands     []string // slash commands invoked (names without leading slash)
	MCPTools     []string // distinct MCP tool names used (mcp__server__tool)
	UsedTask     bool     // a Task subagent was spawned
	EditedFiles  []string // files touched by Edit/Write/NotebookEdit
	GitMutations int      // count of `git add|commit|diff` Bash calls

	// Images are screenshots/pasted images embedded in user turns (base64). They
	// are recoverable; a prompt that only names an image *path* is not (the bytes
	// were never stored), which the filter treats differently.
	Images []Image

	FirstTS string
	LastTS  string
}

// Image is a decoded reference image extracted from a user turn.
type Image struct {
	Ext  string // file extension incl. dot, e.g. ".png"
	Data []byte
}

// raw mirrors the fields we read from each JSONL line. Unknown fields ignored.
type rawEntry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	GitBranch string `json:"gitBranch"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// block is one element of a message.content array.
type block struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Name   string `json:"name"` // tool_use
	Source struct {
		Type      string `json:"type"`       // "base64"
		MediaType string `json:"media_type"` // e.g. "image/png"
		Data      string `json:"data"`       // base64-encoded bytes
	} `json:"source"` // image
	Input struct {
		Command  string `json:"command"`   // Bash
		FilePath string `json:"file_path"` // Edit/Write/NotebookEdit
	} `json:"input"`
}

var (
	commandRe = regexp.MustCompile(`<command-name>\s*/?([^<]+?)\s*</command-name>`)
	gitMutRe  = regexp.MustCompile(`\bgit\s+(?:-C\s+\S+\s+)?(?:add|commit|diff)\b`)
	mcpPrefix = "mcp__"
)

// ParseFile reads and distills a session JSONL file.
func ParseFile(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse distills a session from a JSONL stream.
func Parse(r io.Reader) (*Session, error) {
	s := &Session{}
	branches := map[string]bool{}
	mcp := map[string]bool{}
	edited := map[string]bool{}
	cmds := map[string]bool{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024) // sessions can have huge lines
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e rawEntry
		if json.Unmarshal(line, &e) != nil {
			continue // skip malformed lines rather than failing the whole capture
		}
		if e.Timestamp != "" {
			if s.FirstTS == "" {
				s.FirstTS = e.Timestamp
			}
			s.LastTS = e.Timestamp
		}
		if e.GitBranch != "" {
			branches[e.GitBranch] = true
		}
		if e.Cwd != "" {
			s.Cwd = e.Cwd
		}
		switch e.Type {
		case "ai-title":
			if e.Title != "" {
				s.Title = e.Title
			}
		case "user":
			s.ingestUser(e.Message.Content, cmds)
		case "assistant":
			s.ingestAssistant(e.Message.Content, mcp, edited)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	s.Branches = sortedKeys(branches)
	s.MCPTools = sortedKeys(mcp)
	s.EditedFiles = sortedKeys(edited)
	s.Commands = sortedKeys(cmds)
	return s, nil
}

// ingestUser pulls a free-text prompt, slash-command name, or embedded image
// out of a user turn.
func (s *Session) ingestUser(content json.RawMessage, cmds map[string]bool) {
	for _, b := range blocks(content) {
		if b.Type == "image" && b.Source.Type == "base64" && b.Source.Data != "" {
			if data, err := base64.StdEncoding.DecodeString(b.Source.Data); err == nil {
				s.Images = append(s.Images, Image{Ext: extFor(b.Source.MediaType), Data: data})
			}
		}
	}
	text := userText(content)
	if text == "" {
		return
	}
	if m := commandRe.FindAllStringSubmatch(text, -1); m != nil {
		for _, g := range m {
			cmds[strings.ToLower(strings.TrimSpace(g[1]))] = true
		}
		return // a command invocation is bookkeeping, not a task prompt
	}
	if isNoise(text) {
		return
	}
	s.Prompts = append(s.Prompts, strings.TrimSpace(text))
}

// ingestAssistant records tool usage that predicts portability problems.
func (s *Session) ingestAssistant(content json.RawMessage, mcp, edited map[string]bool) {
	for _, b := range blocks(content) {
		if b.Type != "tool_use" {
			continue
		}
		switch {
		case strings.HasPrefix(b.Name, mcpPrefix):
			mcp[b.Name] = true
		case b.Name == "Task":
			s.UsedTask = true
		case b.Name == "Edit" || b.Name == "Write" || b.Name == "NotebookEdit":
			if b.Input.FilePath != "" {
				edited[b.Input.FilePath] = true
			}
		case b.Name == "Bash":
			if gitMutRe.MatchString(b.Input.Command) {
				s.GitMutations++
			}
		}
	}
}

// userText flattens a user message's content to plain text. Content is either a
// JSON string or an array of blocks; tool_result blocks carry no task intent.
func userText(content json.RawMessage) string {
	var str string
	if json.Unmarshal(content, &str) == nil {
		return str
	}
	var parts []string
	for _, b := range blocks(content) {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// extFor maps an image media type to a file extension, defaulting to .png.
func extFor(mediaType string) string {
	switch mediaType {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func blocks(content json.RawMessage) []block {
	var bs []block
	_ = json.Unmarshal(content, &bs)
	return bs
}

// noisePrefixes are the harness-injected message shapes that are never user task
// prompts (slash-command echoes, hook feedback, skill boilerplate, reminders).
var noisePrefixes = []string{
	"base directory for this skill",
	"a session-scoped stop hook",
	"stop hook feedback",
	"caveat:",
	"<local-command",
	"<system-reminder",
	"<task-notification",
	"<command-",
	"<post-system",
	"<user-prompt-submit",
	"[request interrupted",
}

func isNoise(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return true
	}
	for _, p := range noisePrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// small sets; simple insertion-free sort
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
