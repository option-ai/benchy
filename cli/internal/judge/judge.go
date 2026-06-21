// Package judge runs a model as a blind grader of an agent's work. The judge is
// given the task (the user prompts), the optional feedback rubric, and the work
// (diff and/or written answer). It is NOT told which agent or model produced
// it, never sees the agent's reasoning, and runs in an empty directory so it
// cannot inspect the workspace. That blindness is the point.
package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/option-ai/benchy/cli/internal/adapter"
	"github.com/option-ai/benchy/cli/internal/score"
)

// maxDiffBytes caps how much diff the judge sees. Beyond this we truncate with
// a marker: an unbounded diff blows the judge's context (and argv limits)
// silently, which is worse than an honest cut.
const maxDiffBytes = 80_000

// maxOutputBytes caps the agent's written answer shown to the judge.
const maxOutputBytes = 40_000

// Input is everything the judge is allowed to see.
type Input struct {
	Task      []string // the recorded user prompts, in replay order
	Responses []string // the agent's response to each turn (len 1 for oneshot)
	Feedback  string   // optional rubric note from the snapshot
	Diff      string   // unified diff the agent produced (may be empty)
	Expects   string   // declared deliverable: diff|answer|conversation|"" (auto)
}

type rawScore struct {
	TaskCompletion    float64 `json:"task_completion"`
	Correctness       float64 `json:"correctness"`
	FeedbackAdherence float64 `json:"feedback_adherence"`
	ScopeDiscipline   float64 `json:"scope_discipline"`
	Rationale         string  `json:"rationale"`
}

// Judge grades the work, sampling `samples` times and taking the per-dimension
// median for stability. The rationale returned is the last sample's (a single
// representative explanation; medians don't have one).
func Judge(ctx context.Context, ref adapter.ModelRef, in Input, samples int, timeout time.Duration) (score.Subscores, string, error) {
	if samples < 1 {
		samples = 1
	}
	prompt := buildPrompt(in)

	var (
		tc, co, fa, sd []float64
		rationale      string
	)
	for i := 0; i < samples; i++ {
		out, err := generate(ctx, ref, prompt, timeout)
		if err != nil {
			return score.Subscores{}, "", fmt.Errorf("judge invoke: %w", err)
		}
		rs, err := parse(out)
		if err != nil {
			return score.Subscores{}, "", fmt.Errorf("judge output: %w", err)
		}
		tc = append(tc, rs.TaskCompletion)
		co = append(co, rs.Correctness)
		fa = append(fa, rs.FeedbackAdherence)
		sd = append(sd, rs.ScopeDiscipline)
		rationale = rs.Rationale
	}
	return score.Subscores{
		TaskCompletion:    score.Median(tc),
		Correctness:       score.Median(co),
		FeedbackAdherence: score.Median(fa),
		ScopeDiscipline:   score.Median(sd),
	}, rationale, nil
}

// truncate cuts s at limit bytes on a line boundary, appending a marker.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return cut + fmt.Sprintf("\n... [truncated: %d bytes omitted]", len(s)-len(cut))
}

func buildPrompt(in Input) string {
	var b strings.Builder
	b.WriteString(`You are a strict, impartial judge in an LLM benchmark.
You are shown a recorded CONVERSATION (pre-scripted user turns, with the
candidate agent's actual responses) and the DIFF of any file changes it made.
You do NOT know which model or tool produced this. Judge only what you see.

The user turns were pre-recorded: they do not react to the agent's responses.
Do not penalize conversational incoherence caused by the script; judge what
the agent did with the information it had at each turn.

`)
	switch in.Expects {
	case "diff":
		b.WriteString("EXPECTED DELIVERABLE: a code change. Judge the diff; responses are commentary. No meaningful diff means task_completion near 0.\n\n")
	case "answer":
		b.WriteString("EXPECTED DELIVERABLE: a written answer. Judge the final response's content. Unrequested file changes count against scope_discipline.\n\n")
	case "conversation":
		b.WriteString("EXPECTED DELIVERABLE: conversational behavior across the whole exchange — e.g. whether the agent raised problems at the right moment, pushed back on wrong premises, asked the right questions. Judge the transcript turn by turn against the feedback note.\n\n")
	default:
		b.WriteString("EXPECTED DELIVERABLE: infer it from the task — a question expects an answer, an instruction to change code expects a diff — and judge accordingly.\n\n")
	}
	b.WriteString(`Score four dimensions from 0 to 100:
- task_completion: did the work actually accomplish what the task asked?
- correctness: is it correct, sound, and free of bugs or errors?
- feedback_adherence: does it satisfy the reviewer's feedback note? If no note is given, score this 100.
- scope_discipline: did it stay on-task without unrelated or destructive changes?

Respond with ONLY a JSON object, no prose around it:
{"task_completion":<n>,"correctness":<n>,"feedback_adherence":<n>,"scope_discipline":<n>,"rationale":"<one paragraph>"}

== REVIEWER FEEDBACK NOTE ==
`)
	if strings.TrimSpace(in.Feedback) == "" {
		b.WriteString("(none provided — score feedback_adherence 100)\n")
	} else {
		b.WriteString(in.Feedback + "\n")
	}
	b.WriteString("\n== CONVERSATION ==\n")
	perTurn := maxOutputBytes
	if len(in.Responses) > 1 {
		perTurn = maxOutputBytes / len(in.Responses)
	}
	for i, t := range in.Task {
		fmt.Fprintf(&b, "[user %d]\n%s\n\n", i+1, t)
		if i < len(in.Responses) {
			resp := strings.TrimSpace(in.Responses[i])
			if resp == "" {
				resp = "(no response captured)"
			}
			fmt.Fprintf(&b, "[agent %d]\n%s\n\n", i+1, truncate(resp, perTurn))
		}
	}
	if strings.TrimSpace(in.Diff) != "" {
		b.WriteString("== DIFF (files changed/created) ==\n")
		b.WriteString(truncate(in.Diff, maxDiffBytes) + "\n")
	} else {
		b.WriteString("== DIFF ==\n(no file changes)\n")
	}
	return b.String()
}

var jsonRe = regexp.MustCompile(`(?s)\{.*\}`)

func parse(out string) (rawScore, error) {
	m := jsonRe.FindString(out)
	if m == "" {
		return rawScore{}, fmt.Errorf("no JSON object in judge output")
	}
	var rs rawScore
	if err := json.Unmarshal([]byte(m), &rs); err != nil {
		return rawScore{}, err
	}
	return rs, nil
}

// generate invokes a model purely as a text generator and returns its answer.
// The prompt goes via stdin (argv has hard size limits), and the command runs
// in a fresh empty directory so a tool-capable judge cannot inspect any
// workspace or repo.
func generate(ctx context.Context, ref adapter.ModelRef, prompt string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	neutral, err := os.MkdirTemp("", "bench-judge-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(neutral)

	var name string
	var args []string
	var lastMsgFile string
	switch ref.Agent {
	case "claude-code":
		// prompt on stdin when -p has no argument
		name, args = "claude", []string{"-p", "--model", ref.Model, "--setting-sources", "project"}
	case "codex":
		// "-" reads the prompt from stdin; clean answer via --output-last-message
		lastMsgFile = filepath.Join(neutral, "last-message.txt")
		name, args = "codex", []string{"exec", "--model", ref.Model,
			"--skip-git-repo-check", "--output-last-message", lastMsgFile, "-"}
	case "cursor-agent":
		name, args = "cursor-agent", []string{"-p", prompt, "--model", ref.Model, "--output-format", "text"}
	case "opencode":
		name, args = "opencode", []string{"run", "--model", ref.Model, prompt}
	default:
		return "", fmt.Errorf("unknown judge agent %q", ref.Agent)
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = neutral
	cmd.Env = os.Environ()
	if ref.Agent == "claude-code" || ref.Agent == "codex" {
		cmd.Stdin = strings.NewReader(prompt)
	}
	out, err := cmd.Output()
	if err != nil {
		return string(out), err
	}
	if lastMsgFile != "" {
		if msg, rerr := os.ReadFile(lastMsgFile); rerr == nil && len(msg) > 0 {
			return string(msg), nil
		}
	}
	return string(out), nil
}
