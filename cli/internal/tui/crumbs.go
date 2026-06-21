package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Wizard steps for `benchy run`, mirrored from the benchy.run interactive demo:
// evals › models › judge › run › results.
const (
	StepEvals = iota
	StepModels
	StepJudge
	StepRun
	StepResults
)

var stepLabels = [...]string{"evals", "models", "judge", "run", "results"}

var (
	stCrumbDone = lipgloss.NewStyle().Foreground(cGood)
	stCrumbCur  = lipgloss.NewStyle().Bold(true).Foreground(cCarrot)
	stCrumbNext = lipgloss.NewStyle().Foreground(cDim)
	stCrumbSep  = lipgloss.NewStyle().Foreground(cDim)
)

// Crumbs renders the wizard breadcrumb with the demo's vocabulary: completed
// steps get a green ✓, the current step a carrot ●, future steps a dim ○.
func Crumbs(cur int) string {
	parts := make([]string, 0, 2*len(stepLabels)-1)
	for i, l := range stepLabels {
		if i > 0 {
			parts = append(parts, stCrumbSep.Render(" › "))
		}
		switch {
		case i < cur:
			parts = append(parts, stCrumbDone.Render("✓ "+l))
		case i == cur:
			parts = append(parts, stCrumbCur.Render("● "+l))
		default:
			parts = append(parts, stCrumbNext.Render("○ "+l))
		}
	}
	return strings.Join(parts, "")
}
