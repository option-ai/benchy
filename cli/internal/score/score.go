// Package score turns a judge's rubric sub-scores plus deterministic gate
// outcomes into a single composite number per (eval x model), and aggregates
// those into one number per model across a benchy run.
//
// composite = judge_overall * gate_factor
//
//   - judge_overall is the rubric-weighted mean of the four sub-scores (0..100).
//   - gate_factor starts at 1.0; a build failure caps it hard (default 0.30),
//     test failures scale it by the pass ratio, lint failure applies a small
//     penalty. It is clamped to [0,1].
//
// So a beautiful diff that doesn't compile cannot beat an ugly one that does.
package score

import (
	"sort"

	"github.com/option-ai/benchy/cli/internal/config"
)

// Subscores are the judge's four rubric dimensions, each 0..100.
type Subscores struct {
	TaskCompletion    float64 `json:"task_completion"`
	Correctness       float64 `json:"correctness"`
	FeedbackAdherence float64 `json:"feedback_adherence"`
	ScopeDiscipline   float64 `json:"scope_discipline"`
}

// GateOutcome is a binary gate (build, lint). Ran=false means the snapshot did
// not define the command, so it is ignored in scoring.
type GateOutcome struct {
	Ran    bool `json:"ran"`
	Passed bool `json:"passed"`
}

// TestOutcome is the test gate. Binary for now: bench does not parse
// per-framework pass counts, so it does not pretend to have a ratio.
type TestOutcome struct {
	Ran    bool `json:"ran"`
	Passed bool `json:"passed"`
}

// GateResult bundles the deterministic checks for one run.
type GateResult struct {
	Build GateOutcome `json:"build"`
	Test  TestOutcome `json:"test"`
	Lint  GateOutcome `json:"lint"`
}

// Result is the scored outcome of one (eval x model) run.
type Result struct {
	Eval         string     `json:"eval"`
	Model        string     `json:"model"`
	Subscores    Subscores  `json:"subscores"`
	Gates        GateResult `json:"gates"`
	JudgeOverall float64    `json:"judge_overall"`
	GateFactor   float64    `json:"gate_factor"`
	Composite    float64    `json:"composite"`
	Rationale    string     `json:"rationale,omitempty"`
	Err          string     `json:"error,omitempty"` // non-empty if the run failed outright
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// JudgeOverall is the rubric-weighted mean of the sub-scores. Weights need not
// sum to 1; they are normalized here.
func JudgeOverall(s Subscores, w config.RubricWeights) float64 {
	sum := w.TaskCompletion + w.Correctness + w.FeedbackAdhere + w.ScopeDiscipline
	if sum == 0 {
		return 0
	}
	weighted := s.TaskCompletion*w.TaskCompletion +
		s.Correctness*w.Correctness +
		s.FeedbackAdherence*w.FeedbackAdhere +
		s.ScopeDiscipline*w.ScopeDiscipline
	return weighted / sum
}

// GateFactor folds deterministic checks into a multiplier in [0,1].
func GateFactor(g GateResult, w config.GateWeights) float64 {
	f := 1.0
	if g.Build.Ran && !g.Build.Passed && w.BuildFailCap < f {
		f = w.BuildFailCap // build failure caps hard
	}
	if g.Test.Ran && !g.Test.Passed {
		f *= w.TestFailFactor
	}
	if g.Lint.Ran && !g.Lint.Passed {
		f -= w.LintPenalty
	}
	return clamp(f, 0, 1)
}

// Compute produces the composite Result from sub-scores and gates.
func Compute(eval, model string, sub Subscores, gates GateResult, c config.Config) Result {
	jo := JudgeOverall(sub, c.Rubric)
	gf := GateFactor(gates, c.Gates)
	return Result{
		Eval:         eval,
		Model:        model,
		Subscores:    sub,
		Gates:        gates,
		JudgeOverall: round2(jo),
		GateFactor:   round2(gf),
		Composite:    round2(jo * gf),
	}
}

// Median returns the median of xs (used for best-of-N judge sampling).
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// ModelScore is the mean composite across a model's results (its leaderboard
// number). Failed runs score 0 and still count, so flakiness is penalized.
func ModelScore(rs []Result) float64 {
	if len(rs) == 0 {
		return 0
	}
	var sum float64
	for _, r := range rs {
		sum += r.Composite
	}
	return round2(sum / float64(len(rs)))
}

// Leaderboard is models ranked by mean composite, descending.
type LeaderRow struct {
	Model string
	Score float64
	Runs  int
}

// Leaderboard groups results by model and ranks them.
func Leaderboard(rs []Result) []LeaderRow {
	byModel := map[string][]Result{}
	for _, r := range rs {
		byModel[r.Model] = append(byModel[r.Model], r)
	}
	var rows []LeaderRow
	for m, list := range byModel {
		rows = append(rows, LeaderRow{Model: m, Score: ModelScore(list), Runs: len(list)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].Model < rows[j].Model
		}
		return rows[i].Score > rows[j].Score
	})
	return rows
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
