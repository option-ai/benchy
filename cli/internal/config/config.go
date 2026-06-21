// Package config owns the on-disk layout under ~/.config/benchy and the two
// JSON files we persist: config.json (defaults, rubric weights, model registry)
// and auth.json (provider keys, written 0600).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Dir is the root config directory. Override with $BENCHY_HOME (or the
// legacy $BENCH_HOME) for tests.
func Dir() string {
	if h := os.Getenv("BENCHY_HOME"); h != "" {
		return h
	}
	if h := os.Getenv("BENCH_HOME"); h != "" {
		return h
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "benchy")
	migrateLegacyDir(filepath.Join(base, "bench"), dir)
	return dir
}

// migrateLegacyDir renames the pre-rebrand ~/.config/bench tree to
// ~/.config/benchy, once, the first time the new path is resolved. Best
// effort: if the rename fails (permissions, cross-device), the new dir is
// simply created fresh by EnsureDirs.
func migrateLegacyDir(old, new string) {
	migrateOnce.Do(func() {
		if _, err := os.Stat(new); !os.IsNotExist(err) {
			return
		}
		if fi, err := os.Stat(old); err != nil || !fi.IsDir() {
			return
		}
		_ = os.Rename(old, new)
	})
}

var migrateOnce sync.Once

func SnapshotsDir() string { return filepath.Join(Dir(), "snapshots") }
func CacheDir() string     { return filepath.Join(Dir(), "cache") }
func RunsDir() string      { return filepath.Join(Dir(), "runs") }
func configPath() string   { return filepath.Join(Dir(), "config.json") }
func authPath() string     { return filepath.Join(Dir(), "auth.json") }

// CandidatesDir holds auto-captured evals awaiting review. They are kept out of
// SnapshotsDir so a hook-driven capture never silently enters a `benchy run`
// until a human promotes it.
func CandidatesDir() string { return filepath.Join(Dir(), "candidates") }

// PendingDir holds per-session sidecars written at SessionStart (the base commit
// to anchor a prospective capture against) and read at capture time.
func PendingDir() string { return filepath.Join(Dir(), "pending") }

// EnsureDirs creates the directory tree if missing. Cheap, idempotent.
func EnsureDirs() error {
	for _, d := range []string{Dir(), SnapshotsDir(), CacheDir(), RunsDir(), CandidatesDir(), PendingDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ReplayMode controls how a snapshot's prompts are fed to an agent.
type ReplayMode string

const (
	// ReplayOneShot collapses every user message into a single prompt (default).
	ReplayOneShot ReplayMode = "oneshot"
	// ReplaySequential replays each user message as its own turn, resuming the
	// agent session between turns.
	ReplaySequential ReplayMode = "sequential"
)

// GateWeights tunes how deterministic gates fold into the composite score.
// The composite is judge_overall * gate_factor, where gate_factor starts at 1.0
// and each failing gate applies its penalty. A build failure caps hard.
type GateWeights struct {
	// BuildFailCap is the multiplier applied when the build gate fails.
	BuildFailCap float64 `json:"build_fail_cap"`
	// LintPenalty is subtracted from gate_factor when lint fails.
	LintPenalty float64 `json:"lint_penalty"`
	// TestFailFactor multiplies gate_factor when the test gate fails.
	TestFailFactor float64 `json:"test_fail_factor"`
}

// RubricWeights are the relative weights of the judge's sub-scores. They are
// normalized at scoring time, so they need not sum to 1.
type RubricWeights struct {
	TaskCompletion  float64 `json:"task_completion"`
	Correctness     float64 `json:"correctness"`
	FeedbackAdhere  float64 `json:"feedback_adherence"`
	ScopeDiscipline float64 `json:"scope_discipline"`
}

// Config is the persisted, versioned scoring + run configuration. Version is
// stamped into every run so historical numbers stay comparable.
type Config struct {
	Version       int        `json:"version"`
	DefaultJudge  string     `json:"default_judge"` // e.g. "claude-code:claude-opus-4-8"
	DefaultReplay ReplayMode `json:"default_replay"`
	JudgeSamples  int        `json:"judge_samples"` // best-of-N median; 1 = single
	Concurrency   int        `json:"concurrency"`
	// PerAgentConcurrency caps concurrent jobs per agent CLI, so one provider
	// is never hammered by the whole pool at once (RPM/TPM pressure).
	PerAgentConcurrency int `json:"per_agent_concurrency"`
	// RateLimitRetries is how many times a job is retried (workspace reset,
	// exponential backoff) when its failure looks like a rate limit / quota.
	RateLimitRetries int           `json:"rate_limit_retries"`
	Rubric           RubricWeights `json:"rubric"`
	Gates            GateWeights   `json:"gates"`
	// Models optionally overrides an agent's selectable model list, keyed by
	// agent id (e.g. "claude-code"). Empty/absent => use the agent's built-ins.
	Models map[string][]string `json:"models,omitempty"`
	// DisabledAgents lists agent ids the user opted out of during `benchy
	// setup`; their models are hidden from the run/judge pickers until
	// re-enabled in setup.
	DisabledAgents []string `json:"disabled_agents,omitempty"`
	// LastEvals/LastModels remember the previous run's selections (eval titles
	// and model refs) and pre-select them in the next interactive run.
	LastEvals  []string `json:"last_evals,omitempty"`
	LastModels []string `json:"last_models,omitempty"`
	// ReportResults opts into sending anonymous per-model scores (model name,
	// composite, run count, judge) to the global leaderboard at benchy.run
	// after each run. Prompts, diffs, and repo details are never sent. Default
	// false; enable by setting "report_results": true in config.json.
	ReportResults bool `json:"report_results"`
	// AutoPromoteScore lets prospective capture promote a candidate straight into
	// the run set (snapshots/) when its filter score is >= this value, skipping
	// the candidates review queue. Defaults to 90 (near-pristine only); 70
	// promotes anything the filter rates "good"; 0 disables it so every capture
	// waits for manual `benchy candidates promote`.
	AutoPromoteScore int `json:"auto_promote_score"`
}

// Default returns the baseline config used on first run.
func Default() Config {
	return Config{
		Version:             1,
		DefaultJudge:        "claude-code:claude-opus-4-8",
		DefaultReplay:       ReplayOneShot,
		JudgeSamples:        1,
		Concurrency:         3,
		PerAgentConcurrency: 2,
		RateLimitRetries:    2,
		Rubric: RubricWeights{
			TaskCompletion:  0.40,
			Correctness:     0.30,
			FeedbackAdhere:  0.20,
			ScopeDiscipline: 0.10,
		},
		Gates: GateWeights{
			BuildFailCap:   0.30,
			LintPenalty:    0.10,
			TestFailFactor: 0.50,
		},
		// Near-pristine captures auto-promote into the run set out of the box;
		// 70-89 ("good" with a caveat) still wait in `benchy candidates`. Set to
		// 0 to disable and review everything manually.
		AutoPromoteScore: 90,
	}
}

// Load reads config.json, falling back to defaults (and writing them) if absent.
func Load() (Config, error) {
	b, err := os.ReadFile(configPath())
	if os.IsNotExist(err) {
		c := Default()
		return c, Save(c)
	}
	if err != nil {
		return Config{}, err
	}
	c := Default() // defaults fill any missing fields
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse config.json: %w", err)
	}
	return c, nil
}

// Save writes config.json pretty-printed.
func Save(c Config) error {
	if err := EnsureDirs(); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(configPath(), b, 0o644)
}

// AgentDisabled reports whether the user opted this agent out during setup.
func (c Config) AgentDisabled(id string) bool {
	for _, d := range c.DisabledAgents {
		if d == id {
			return true
		}
	}
	return false
}

// SetAgentDisabled adds or removes id from the disabled list.
func (c *Config) SetAgentDisabled(id string, disabled bool) {
	out := c.DisabledAgents[:0]
	for _, d := range c.DisabledAgents {
		if d != id {
			out = append(out, d)
		}
	}
	if disabled {
		out = append(out, id)
	}
	c.DisabledAgents = out
}
