package transcript

import (
	"fmt"
	"regexp"
	"strings"
)

// Verdict classifies a session's fitness as a benchy eval.
type Verdict string

const (
	// VerdictGood is a clean, portable, single-task session.
	VerdictGood Verdict = "good"
	// VerdictWeak is capturable but compromised (under-specified, no diff, etc.).
	VerdictWeak Verdict = "weak"
	// VerdictReject fails a hard portability rule and is not captured.
	VerdictReject Verdict = "reject"
)

// Result is the filter's judgement of a session.
type Result struct {
	Accept  bool    // false => do not capture
	Verdict Verdict // good | weak | reject
	Score   int     // 0-100 eval-worthiness
	Reasons []string
}

// harmlessCommands are operational/Claude-Code slash commands that don't define
// a task. Their presence is fine. Anything else implies the session was *driven*
// by a non-portable skill/plugin and can't be replayed against other harnesses.
var harmlessCommands = map[string]bool{
	"add-to-benchy": true, "benchy": true, "ship": true, "rename": true,
	"config": true, "clear": true, "login": true, "logout": true, "model": true,
	"fast": true, "compact": true, "cost": true, "help": true, "status": true,
	"memory": true, "init": true, "resume": true, "mcp": true, "doctor": true,
	"bug": true, "vim": true, "terminal-setup": true, "review": true,
}

var (
	imageRe      = regexp.MustCompile(`(?i)\b[\w./-]+\.(png|jpe?g|gif|webp|heic)\b|screenshot`)
	urlRe        = regexp.MustCompile(`https?://[^\s)]+`)
	continuePfx  = []string{"continue from where", "continue from", "pick up where", "carry on from", "resume where"}
	steeringWord = map[string]bool{
		"go": true, "go on": true, "go ahead": true, "yes": true, "y": true,
		"ok": true, "okay": true, "sure": true, "continue": true, "next": true,
		"do it": true, "proceed": true, "done": true, "launch it now": true,
		"go with it": true, "yes ure": true, "yes sure": true, "ship it": true,
	}
)

// Evaluate judges a session. Hard rules set Accept=false (Reject); soft issues
// dock the score and downgrade Good->Weak but still capture.
func Evaluate(s *Session) Result {
	var hard, soft []string

	// --- hard rules: a replay can't reproduce these at all ---
	if len(s.Prompts) == 0 {
		hard = append(hard, "no free-text task prompt (command/skill-driven session)")
	}
	if len(s.MCPTools) > 0 {
		hard = append(hard, fmt.Sprintf("depends on MCP/live-data tools (%s) the replay won't have", mcpServers(s.MCPTools)))
	}
	if bad := nonPortableCommands(s.Commands); len(bad) > 0 {
		hard = append(hard, fmt.Sprintf("driven by non-portable slash command(s): /%s", strings.Join(bad, ", /")))
	}
	// 4+ branches is hopeless sprawl; 2-3 is usually main->feature or two related
	// tasks that a reviewer can split, so it's a soft warning (handled below).
	if len(s.Branches) > 3 {
		hard = append(hard, fmt.Sprintf("spans %d branches — many tasks, not one self-contained eval", len(s.Branches)))
	}
	if len(s.Prompts) > 0 && !hasSubstantive(s.Prompts) {
		hard = append(hard, "only conversational steering, no task specification")
	}
	if dependsOnPriorSession(s.Prompts) {
		hard = append(hard, "depends on prior-session context (\"continue from where you left off\")")
	}

	if len(hard) > 0 {
		return Result{Accept: false, Verdict: VerdictReject, Score: clamp(40 - 12*len(hard)), Reasons: hard}
	}

	// --- soft rules: capturable, but weaker signal ---
	score := 100
	if len(s.Branches) > 1 {
		soft = append(soft, fmt.Sprintf("spans %d branches — likely multiple tasks; split into separate evals before running", len(s.Branches)))
		score -= 35
	}
	if s.UsedTask {
		soft = append(soft, "used subagents (Task) — partial portability risk")
		score -= 10
	}
	if len(s.EditedFiles) == 0 && s.GitMutations == 0 {
		soft = append(soft, "no file edits or git activity — verifiable only as an answer, not a diff")
		score -= 35
	}
	switch {
	case len(s.Images) > 0:
		// Bytes are in the transcript: saved alongside the eval and replayed to
		// the agent. Not a problem — just informational.
		soft = append(soft, fmt.Sprintf("%d reference image(s) captured and replayed to the agent", len(s.Images)))
	case promptMatching(s.Prompts, imageRe) != "":
		// Only a path was referenced; the bytes were never stored and the temp
		// file is gone. The reviewer must inline the relevant detail.
		soft = append(soft, "references an image by path, but its bytes aren't in the transcript — inline the relevant detail before running")
		score -= 15
	}
	if promptMatching(s.Prompts, urlRe) != "" {
		soft = append(soft, "references an external URL — may not be reachable/stable at replay")
		score -= 10
	}
	if len(s.Prompts) == 1 && wordCount(s.Prompts[0]) < 8 {
		soft = append(soft, "single short prompt — likely under-specified")
		score -= 15
	}
	if len(s.Prompts) > 12 {
		soft = append(soft, fmt.Sprintf("%d prompts — long, possibly several tasks", len(s.Prompts)))
		score -= 10
	}

	score = clamp(score)
	v := VerdictGood
	if score < 70 {
		v = VerdictWeak
	}
	return Result{Accept: true, Verdict: v, Score: score, Reasons: soft}
}

func nonPortableCommands(cmds []string) []string {
	var bad []string
	for _, c := range cmds {
		if !harmlessCommands[c] {
			bad = append(bad, c)
		}
	}
	return bad
}

// mcpServers collapses mcp__server__tool names to distinct server ids.
func mcpServers(tools []string) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tools {
		parts := strings.SplitN(strings.TrimPrefix(t, "mcp__"), "__", 2)
		srv := parts[0]
		if !seen[srv] {
			seen[srv] = true
			out = append(out, srv)
		}
	}
	return strings.Join(out, ", ")
}

func hasSubstantive(prompts []string) bool {
	for _, p := range prompts {
		if !isSteering(p) && wordCount(p) >= 5 {
			return true
		}
	}
	return false
}

func isSteering(p string) bool {
	t := strings.ToLower(strings.TrimSpace(strings.TrimRight(p, ".!")))
	if steeringWord[t] {
		return true
	}
	// single token / menu pick ("a", "b", "2", "option c")
	t = strings.TrimPrefix(t, "option ")
	return len(t) <= 2
}

func dependsOnPriorSession(prompts []string) bool {
	for _, p := range prompts {
		t := strings.ToLower(strings.TrimSpace(p))
		for _, pfx := range continuePfx {
			if strings.HasPrefix(t, pfx) {
				return true
			}
		}
	}
	return false
}

func promptMatching(prompts []string, re *regexp.Regexp) string {
	for _, p := range prompts {
		if re.MatchString(p) {
			return p
		}
	}
	return ""
}

func wordCount(s string) int { return len(strings.Fields(s)) }

func clamp(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}
