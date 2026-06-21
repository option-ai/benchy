// Package adapter abstracts a coding agent CLI behind a tiny interface. The
// runner collapses prompts (oneshot -> one turn) before calling Run, so an
// adapter only ever iterates turns and never needs to know the replay mode.
// bench captures the resulting git diff itself, keeping this contract minimal.
package adapter

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Budget bounds an agent run. Timeout covers the WHOLE run (all turns), not
// each turn individually.
type Budget struct {
	Timeout  time.Duration
	MaxTurns int // 0 = unlimited (advisory; not all agents honor it)
}

// WithTimeout applies the whole-run budget to a context. Call once per Run.
func (b Budget) WithTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if b.Timeout > 0 {
		return context.WithTimeout(ctx, b.Timeout)
	}
	return ctx, func() {}
}

// AuthInfo describes how an agent authenticates. Every supported agent uses its
// own login (its own CLI / subscription), not a bench-managed API key.
type AuthInfo struct {
	// LoginCmd is an executable command that logs the user in, e.g.
	// "codex login". Empty when login is interactive inside the tool itself.
	LoginCmd string
	// Note is a one-line human explanation shown during setup.
	Note string
}

// Agent is a coding CLI bench can drive headlessly.
type Agent interface {
	// ID is the stable adapter id, e.g. "claude-code".
	ID() string
	// Available reports whether the binary is installed and usable.
	Available() bool
	// Auth describes how this agent authenticates (its own login).
	Auth() AuthInfo
	// Models lists the model ids selectable for this agent.
	Models() []string
	// Run executes the agent in dir against turns, leaving edits in the working
	// tree. turns has length 1 for oneshot replay. It returns the agent's
	// response to each turn (the full replay transcript on the agent side), so
	// answer- and conversation-shaped evals are judgeable; on error it returns
	// the responses collected so far.
	Run(ctx context.Context, dir string, turns []string, model string, b Budget) ([]string, error)
}

var registry []Agent

// Register adds an agent to the global registry.
func Register(a Agent) { registry = append(registry, a) }

// All returns every registered agent.
func All() []Agent { return registry }

// Get returns the agent with the given id, or nil.
func Get(id string) Agent {
	for _, a := range registry {
		if a.ID() == id {
			return a
		}
	}
	return nil
}

// ModelRef is a concrete (agent, model) pair addressable as "agent:model".
type ModelRef struct {
	Agent string
	Model string
}

// Ref is the canonical "agent:model" string.
func (m ModelRef) Ref() string { return m.Agent + ":" + m.Model }

// ParseRef splits "agent:model" into a ModelRef.
func ParseRef(s string) (ModelRef, error) {
	i := strings.Index(s, ":")
	if i < 0 {
		return ModelRef{}, fmt.Errorf("invalid model ref %q (want agent:model)", s)
	}
	return ModelRef{Agent: s[:i], Model: s[i+1:]}, nil
}

// AvailableModels returns every (agent, model) ref for installed agents using
// each agent's built-in model list.
func AvailableModels() []ModelRef { return AvailableModelsWith(nil) }

// AvailableModelsWith is like AvailableModels but lets config.json override an
// agent's model list (keyed by agent id). An override fully replaces the
// built-in list for that agent, so users can add models (e.g. a new Claude
// release) without waiting on a code change.
func AvailableModelsWith(overrides map[string][]string) []ModelRef {
	var out []ModelRef
	for _, a := range registry {
		if !a.Available() {
			continue
		}
		models := a.Models()
		if ov, ok := overrides[a.ID()]; ok && len(ov) > 0 {
			models = ov
		}
		for _, m := range models {
			out = append(out, ModelRef{Agent: a.ID(), Model: m})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	return out
}

// onPath reports whether bin is resolvable on PATH.
func onPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// run executes a command in dir, returning combined output. Timeout is the
// caller's job (Budget.WithTimeout, applied once per whole run).
func run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07`)

// stripANSI removes terminal escape sequences so captured agent output is plain
// text — both for the judge (no tool-fingerprinting control codes) and for the
// persisted artifacts.
func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }
