// Package runner orchestrates a benchy run: clone each repo once, add a detached
// worktree per (eval x model) at the eval's commit, drive the agent, capture
// the diff, run deterministic gates, then hand the diff to a blind judge and
// fold everything into a composite score.
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/option-ai/benchy/cli/internal/adapter"
	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/judge"
	"github.com/option-ai/benchy/cli/internal/score"
	"github.com/option-ai/benchy/cli/internal/snapshot"
)

// Stage marks where a job is in the pipeline (for the TUI status grid).
type Stage string

const (
	StageQueued Stage = "queued"
	StageClone  Stage = "clone"
	StageAgent  Stage = "agent"
	StageGates  Stage = "gates"
	StageJudge  Stage = "judge"
	StageRetry  Stage = "retry"
	StageDone   Stage = "done"
	StageError  Stage = "error"
)

// Label is the human-friendly name shown in the UI. It avoids leaking internal
// terms and stays accurate for scratch evals (which set up a workspace rather
// than cloning).
func (s Stage) Label() string {
	switch s {
	case StageQueued:
		return "queued"
	case StageClone:
		return "preparing workspace"
	case StageAgent:
		return "running agent"
	case StageGates:
		return "running checks"
	case StageJudge:
		return "judging"
	case StageRetry:
		return "rate-limited, retrying"
	case StageDone:
		return "done"
	case StageError:
		return "error"
	}
	return string(s)
}

// Event is a progress update for one job.
type Event struct {
	Eval  string
	Model string
	Stage Stage
	Err   error
}

// Options configures a run.
type Options struct {
	Evals       []*snapshot.Snapshot
	Models      []adapter.ModelRef
	Judge       adapter.ModelRef
	Cfg         config.Config
	AgentBudget adapter.Budget
	JudgeTO     time.Duration
	Now         time.Time    // injected so the package stays deterministic/testable
	Events      chan<- Event // optional; nil to disable progress
}

// RunResult is the persisted outcome of a whole run.
type RunResult struct {
	ID          string            `json:"id"`
	StartedAt   string            `json:"started_at"`
	ConfigVer   int               `json:"config_version"`
	Judge       string            `json:"judge"`
	Results     []score.Result    `json:"results"`
	Leaderboard []score.LeaderRow `json:"leaderboard"`
	Dir         string            `json:"-"`
}

func emit(ch chan<- Event, e Event) {
	if ch != nil {
		ch <- e
	}
}

// Run executes the bench and returns the scored, persisted result.
func Run(ctx context.Context, o Options) (*RunResult, error) {
	if err := config.EnsureDirs(); err != nil {
		return nil, err
	}
	runID := fmt.Sprintf("%s-%03d", o.Now.Format("2006-01-02T15-04-05"), o.Now.Nanosecond()/1e6)
	runDir := filepath.Join(config.RunsDir(), runID)
	// Workspaces live under a ".claude/worktrees" path segment on purpose:
	// Claude Code setups commonly enforce "isolate into a worktree unless
	// already under .claude/worktrees/" via hooks or global instructions, and
	// an agent that obeys mid-eval would do its work in a nested worktree the
	// diff capture can't see. This path makes such policies treat the job
	// workspace as already isolated.
	work := filepath.Join(runDir, "work", ".claude", "worktrees")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}

	// Clone each distinct repo once into the run's cache, reused across jobs.
	clones := map[string]string{}
	var cloneMu sync.Mutex
	cloneRepo := func(repo, sourcePath string) (string, error) {
		cloneMu.Lock()
		defer cloneMu.Unlock()
		if p, ok := clones[repo]; ok {
			return p, nil
		}
		dest := filepath.Join(config.CacheDir(), snapshot.Slug(repo))
		if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
			if err := gitClone(ctx, repo, sourcePath, dest); err != nil {
				return "", err
			}
		} else {
			_ = gitFetch(ctx, dest) // refresh existing cache, best-effort
		}
		clones[repo] = dest
		return dest, nil
	}

	// Serialize worktree adds per cache repo: concurrent `git worktree add`
	// on one repo contends on git's internal locks.
	wtLocks := map[string]*sync.Mutex{}
	var wtLockMu sync.Mutex
	lockFor := func(repo string) *sync.Mutex {
		wtLockMu.Lock()
		defer wtLockMu.Unlock()
		if l, ok := wtLocks[repo]; ok {
			return l
		}
		l := &sync.Mutex{}
		wtLocks[repo] = l
		return l
	}

	// Build the job matrix.
	type job struct {
		eval  *snapshot.Snapshot
		model adapter.ModelRef
	}
	var jobs []job
	for _, e := range o.Evals {
		for _, m := range o.Models {
			jobs = append(jobs, job{e, m})
		}
	}

	conc := o.Cfg.Concurrency
	if conc < 1 {
		conc = 1
	}
	sem := make(chan struct{}, conc)

	// Per-agent semaphores keep any single provider from absorbing the whole
	// pool at once (RPM/TPM pressure shows up as rate-limit failures).
	perAgent := o.Cfg.PerAgentConcurrency
	if perAgent < 1 {
		perAgent = 1
	}
	agentSems := map[string]chan struct{}{}
	for _, m := range o.Models {
		if _, ok := agentSems[m.Agent]; !ok {
			agentSems[m.Agent] = make(chan struct{}, perAgent)
		}
	}
	results := make([]score.Result, len(jobs))
	var wg sync.WaitGroup

	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			as := agentSems[j.model.Agent]
			as <- struct{}{}
			defer func() { <-as }()
			results[i] = runJob(ctx, o, j.eval, j.model, work, runDir, cloneRepo, lockFor)
		}(i, j)
	}
	wg.Wait()
	_ = os.RemoveAll(filepath.Join(runDir, "work")) // jobs cleaned their trees; drop the shell

	res := &RunResult{
		ID:          runID,
		StartedAt:   o.Now.Format(time.RFC3339),
		ConfigVer:   o.Cfg.Version,
		Judge:       o.Judge.Ref(),
		Results:     results,
		Leaderboard: score.Leaderboard(results),
		Dir:         runDir,
	}
	if err := persist(runDir, res); err != nil {
		return res, err
	}
	return res, nil
}

func runJob(ctx context.Context, o Options, e *snapshot.Snapshot, m adapter.ModelRef, work, runDir string, cloneRepo func(string, string) (string, error), lockFor func(string) *sync.Mutex) score.Result {
	r := score.Result{Eval: e.Title, Model: m.Ref()}
	fail := func(stage Stage, err error) score.Result {
		emit(o.Events, Event{e.Title, m.Ref(), StageError, err})
		r.Err = fmt.Sprintf("%s: %v", stage, err)
		return r
	}

	// 1. set up the workspace: clone+worktree for repo-backed evals, or a fresh
	// git-initialised scratch dir for evals captured without a repo.
	emit(o.Events, Event{e.Title, m.Ref(), StageClone, nil})
	wt := filepath.Join(work, snapshot.Slug(e.Title)+"__"+snapshot.Slug(m.Ref()))
	var cache string
	if e.IsScratch() {
		if err := scratchWorkspace(ctx, wt); err != nil {
			return fail(StageClone, err)
		}
	} else {
		var err error
		cache, err = cloneRepo(e.Repo, e.SourcePath)
		if err != nil {
			return fail(StageClone, err)
		}
		l := lockFor(cache)
		l.Lock()
		err = gitWorktreeAdd(ctx, cache, wt, e.Commit)
		l.Unlock()
		if err != nil {
			return fail(StageClone, err)
		}
	}
	// The worktree only holds intermediate state; the diff + output artifacts
	// are persisted under the run dir, so always clean the tree up.
	defer cleanupWorkspace(cache, wt)

	// 2. drive the agent. Collapse prompts for oneshot replay. Capture its final
	// written output so text-answer evals are gradable even with no file changes.
	emit(o.Events, Event{e.Title, m.Ref(), StageAgent, nil})
	ag := adapter.Get(m.Agent)
	if ag == nil || !ag.Available() {
		return fail(StageAgent, fmt.Errorf("agent %q unavailable", m.Agent))
	}
	turns := buildTurns(e, o.Cfg, e.ImagePaths())
	responses, err := ag.Run(ctx, wt, turns, m.Model, o.AgentBudget)
	for attempt := 1; err != nil && attempt <= o.Cfg.RateLimitRetries && looksRateLimited(strings.Join(responses, "\n"), err); attempt++ {
		emit(o.Events, Event{e.Title, m.Ref(), StageRetry, nil})
		if !sleepCtx(ctx, backoff(attempt)) {
			break // run cancelled while waiting
		}
		if rerr := resetWorkspace(ctx, e, wt); rerr != nil {
			break // can't get a clean tree; surface the original error
		}
		responses, err = ag.Run(ctx, wt, turns, m.Model, o.AgentBudget)
	}
	if err != nil {
		if looksRateLimited(strings.Join(responses, "\n"), err) {
			return fail(StageAgent, fmt.Errorf("rate-limited (retries exhausted): %w", err))
		}
		return fail(StageAgent, err)
	}

	// 3. capture the diff (including new files; empty for pure text answers)
	// and persist both artifacts so the run is inspectable after cleanup.
	diff, err := gitCaptureDiff(ctx, wt)
	if err != nil {
		return fail(StageAgent, err)
	}
	saveArtifacts(runDir, e.Title, m.Ref(), diff, renderTranscript(turns, responses))

	// 4. deterministic gates.
	emit(o.Events, Event{e.Title, m.Ref(), StageGates, nil})
	gates := runGates(ctx, wt, e.Gates, o.AgentBudget.Timeout)

	// 5. blind judge.
	emit(o.Events, Event{e.Title, m.Ref(), StageJudge, nil})
	sub, rationale, err := judge.Judge(ctx, o.Judge, judge.Input{
		Task: turns, Responses: responses, Feedback: e.Feedback,
		Diff: diff, Expects: e.Expects,
	}, o.Cfg.JudgeSamples, o.JudgeTO)
	if err != nil {
		return fail(StageJudge, err)
	}

	out := score.Compute(e.Title, m.Ref(), sub, gates, o.Cfg)
	out.Rationale = rationale
	emit(o.Events, Event{e.Title, m.Ref(), StageDone, nil})
	return out
}

// buildTurns collapses prompts into a single turn for oneshot replay, or
// returns them as-is for sequential. An eval without an explicit replay mode
// uses the config default.
func buildTurns(e *snapshot.Snapshot, cfg config.Config, imagePaths []string) []string {
	mode := e.Replay
	if mode == "" {
		mode = cfg.DefaultReplay
	}
	// Conversation evals are about per-turn behavior; collapsing the turns
	// would destroy the very thing being judged.
	if e.Expects == "conversation" {
		mode = config.ReplaySequential
	}
	if mode == config.ReplaySequential {
		turns := append([]string(nil), e.Prompts...)
		if len(turns) > 0 {
			turns[0] += imageRefs(imagePaths)
		}
		return turns
	}
	var b strings.Builder
	b.WriteString("Complete the following request(s) in this repository.\n\n")
	for i, p := range e.Prompts {
		fmt.Fprintf(&b, "--- Message %d ---\n%s\n\n", i+1, p)
	}
	return []string{strings.TrimSpace(b.String()) + imageRefs(imagePaths)}
}

// imageRefs appends a reference-image section pointing at the eval's saved
// screenshots by absolute path. Agents that can read image files (e.g. Claude
// Code) view them; the paths sit outside the worktree so they never enter the
// judged diff. Returns "" when there are no images.
func imageRefs(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n--- Reference image(s) for this task ---\n")
	b.WriteString("This task refers to the following image(s). Open them with your file-reading tool before proceeding:\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	return b.String()
}

// runGates executes the snapshot's build/test/lint commands in the worktree.
func runGates(ctx context.Context, dir string, g snapshot.Gates, timeout time.Duration) score.GateResult {
	var res score.GateResult
	if g.Build != "" {
		ok := shellOK(ctx, dir, g.Build, timeout)
		res.Build = score.GateOutcome{Ran: true, Passed: ok}
	}
	if g.Test != "" {
		ok := shellOK(ctx, dir, g.Test, timeout)
		res.Test = score.TestOutcome{Ran: true, Passed: ok}
	}
	if g.Lint != "" {
		ok := shellOK(ctx, dir, g.Lint, timeout)
		res.Lint = score.GateOutcome{Ran: true, Passed: ok}
	}
	return res
}

// shellOK runs a command string via the shell and reports zero exit.
func shellOK(ctx context.Context, dir, command string, timeout time.Duration) bool {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	return cmd.Run() == nil
}

// renderTranscript interleaves recorded turns with the agent's responses for
// the persisted artifact.
func renderTranscript(turns, responses []string) string {
	var b strings.Builder
	for i, t := range turns {
		fmt.Fprintf(&b, "[user %d]\n%s\n\n", i+1, t)
		if i < len(responses) {
			fmt.Fprintf(&b, "[agent %d]\n%s\n\n", i+1, responses[i])
		}
	}
	return b.String()
}
