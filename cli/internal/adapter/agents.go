package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/option-ai/benchy/cli/internal/config"
)

// Model lists are a menu, not a source of truth: codex and opencode are read
// from the tools themselves; the rest are editable defaults. Any agent's list
// can be overridden via the "models" map in config.json.

func init() {
	Register(&claudeCode{})
	Register(&codex{})
	Register(&cursorAgent{})
	Register(&openCode{})
}

// ---- Claude Code -----------------------------------------------------------

type claudeCode struct{}

func (claudeCode) ID() string      { return "claude-code" }
func (claudeCode) Available() bool { return onPath("claude") }
func (claudeCode) Models() []string {
	return []string{"claude-fable-5", "claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5"}
}
func (claudeCode) Auth() AuthInfo {
	return AuthInfo{Note: "Uses your Claude Code login/subscription. Log in by running `claude` and using /login (or `claude setup-token`)."}
}

func (c *claudeCode) Run(ctx context.Context, dir string, turns []string, model string, b Budget) ([]string, error) {
	ctx, cancel := b.WithTimeout(ctx)
	defer cancel()
	// First turn starts a fresh session; later turns resume it so sequential
	// replay keeps conversation memory. claude -p output is the final message.
	// --setting-sources project: the user's personal global CLAUDE.md must not
	// leak into a benchmark run (it varies per user and can redirect the agent,
	// e.g. a global worktree policy). The repo's own CLAUDE.md still loads.
	var rs []string
	for i, t := range turns {
		args := []string{"-p", t, "--model", model, "--dangerously-skip-permissions", "--setting-sources", "project"}
		if i > 0 {
			args = append(args, "--continue")
		}
		out, err := run(ctx, dir, "claude", args...)
		rs = append(rs, strings.TrimSpace(stripANSI(string(out))))
		if err != nil {
			return rs, err
		}
	}
	return rs, nil
}

// ---- Codex CLI -------------------------------------------------------------

type codex struct{}

func (codex) ID() string      { return "codex" }
func (codex) Available() bool { return onPath("codex") }
func (codex) Models() []string {
	if m := codexCachedModels(); len(m) > 0 {
		return m
	}
	return []string{"gpt-5.5"} // safe fallback
}
func (codex) Auth() AuthInfo {
	return AuthInfo{LoginCmd: "codex login", Note: "Uses your ChatGPT/Codex login — not an API key."}
}

func (c *codex) Run(ctx context.Context, dir string, turns []string, model string, b Budget) ([]string, error) {
	ctx, cancel := b.WithTimeout(ctx)
	defer cancel()
	// --output-last-message gives the agent's clean final message; codex's
	// stdout banners (hooks, token counts) would otherwise fingerprint the tool
	// to the judge. Later turns resume the same session for conversation memory.
	var rs []string
	for i, t := range turns {
		msgFile := filepath.Join(os.TempDir(), "bench-codex-"+randSuffix())
		args := []string{"exec"}
		if i > 0 {
			args = append(args, "resume", "--last")
		}
		args = append(args, "--model", model,
			"--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check",
			"--output-last-message", msgFile, t)
		_, err := run(ctx, dir, "codex", args...)
		var resp string
		if msg, rerr := os.ReadFile(msgFile); rerr == nil {
			resp = strings.TrimSpace(string(msg))
			_ = os.Remove(msgFile)
		}
		rs = append(rs, resp)
		if err != nil {
			return rs, err
		}
	}
	return rs, nil
}

// codexCachedModels reads the models Codex itself advertises for this account
// (~/.codex/models_cache.json), so the menu matches what the login can actually
// run instead of guessing.
func codexCachedModels() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "models_cache.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	var out []string
	for _, m := range doc.Models {
		if m.Slug != "" && m.Visibility == "list" {
			out = append(out, m.Slug)
		}
	}
	return out
}

// ---- cursor-agent ----------------------------------------------------------

type cursorAgent struct{}

func (cursorAgent) ID() string      { return "cursor-agent" }
func (cursorAgent) Available() bool { return onPath("cursor-agent") }
func (cursorAgent) Models() []string {
	// cursor-agent has no model-enumeration command, and guessed ids fail at
	// runtime (the codex lesson). "auto" is the only id guaranteed valid; add
	// specific models via the config.json "models" override.
	return []string{"auto"}
}
func (cursorAgent) Auth() AuthInfo {
	return AuthInfo{LoginCmd: "cursor-agent login", Note: "Uses your Cursor login."}
}

func (c *cursorAgent) Run(ctx context.Context, dir string, turns []string, model string, b Budget) ([]string, error) {
	ctx, cancel := b.WithTimeout(ctx)
	defer cancel()
	var rs []string
	for i, t := range turns {
		args := []string{"-p", t, "--model", model, "--force", "--output-format", "text"}
		if i > 0 {
			args = append(args, "--resume")
		}
		out, err := run(ctx, dir, "cursor-agent", args...)
		rs = append(rs, strings.TrimSpace(stripANSI(string(out))))
		if err != nil {
			return rs, err
		}
	}
	return rs, nil
}

// ---- opencode --------------------------------------------------------------

type openCode struct{}

func (openCode) ID() string      { return "opencode" }
func (openCode) Available() bool { return onPath("opencode") }
func (openCode) Models() []string {
	if m := opencodeModels(); len(m) > 0 {
		return m
	}
	return []string{"anthropic/claude-opus-4-8"} // fallback if enumeration fails
}
func (openCode) Auth() AuthInfo {
	return AuthInfo{LoginCmd: "opencode auth login", Note: "Configure providers via opencode's own auth."}
}

func (c *openCode) Run(ctx context.Context, dir string, turns []string, model string, b Budget) ([]string, error) {
	ctx, cancel := b.WithTimeout(ctx)
	defer cancel()
	var rs []string
	for i, t := range turns {
		args := []string{"run", "--model", model}
		if i > 0 {
			args = append(args, "--continue")
		}
		args = append(args, t)
		out, err := run(ctx, dir, "opencode", args...)
		rs = append(rs, strings.TrimSpace(stripANSI(string(out))))
		if err != nil {
			return rs, err
		}
	}
	return rs, nil
}

var opencodeOnce struct {
	sync.Once
	models []string
}

// opencodeModels enumerates models from `opencode models` itself, cached on
// disk for a day (the command takes ~2s) and in-process for the run.
func opencodeModels() []string {
	opencodeOnce.Do(func() {
		cacheFile := filepath.Join(config.CacheDir(), "opencode-models.txt")
		if st, err := os.Stat(cacheFile); err == nil && time.Since(st.ModTime()) < 24*time.Hour {
			if b, err := os.ReadFile(cacheFile); err == nil {
				opencodeOnce.models = splitLines(string(b))
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := run(ctx, "", "opencode", "models")
		if err != nil {
			return
		}
		models := splitLines(stripANSI(string(out)))
		if len(models) == 0 {
			return
		}
		_ = os.MkdirAll(config.CacheDir(), 0o755)
		_ = os.WriteFile(cacheFile, []byte(strings.Join(models, "\n")), 0o644)
		opencodeOnce.models = models
	})
	return opencodeOnce.models
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && strings.Contains(l, "/") {
			out = append(out, l)
		}
	}
	return out
}

// randSuffix is unique enough for temp filenames without importing math/rand.
func randSuffix() string {
	return time.Now().Format("150405.000000000")
}
