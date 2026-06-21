package tui

import "github.com/charmbracelet/lipgloss"

// Adaptive palette: lipgloss auto-detects the terminal background (via termenv)
// and picks the Light or Dark variant, so output stays legible in both themes.
//
// Warm dark-terminal aesthetic from the bench design system (tokens/colors.css,
// mirrored by the benchy.run interactive demo): violet section headers, magenta
// selection cursor, carrot ★/accents, score values graded green/amber/red,
// dim gray metadata.
var (
	cAccent = lipgloss.AdaptiveColor{Light: "#6C4FE0", Dark: "#9D8CFF"} // section headers (violet)
	cStar   = lipgloss.AdaptiveColor{Light: "#ED7032", Dark: "#F6A877"} // ★ winner, accent chip (carrot)
	cPick   = lipgloss.AdaptiveColor{Light: "#C2185B", Dark: "#FF79C6"} // ▸ cursor/selection (magenta)
	cGood   = lipgloss.AdaptiveColor{Light: "#2E7D32", Dark: "#73F59F"} // success / high score
	cWarn   = lipgloss.AdaptiveColor{Light: "#B26A00", Dark: "#FFB454"} // warning / mid score
	cErr    = lipgloss.AdaptiveColor{Light: "#C62828", Dark: "#FF6B6B"} // error / low score
	cDim    = lipgloss.AdaptiveColor{Light: "#6E6E6E", Dark: "#9AA0A6"} // muted
	cWinBg  = lipgloss.AdaptiveColor{Light: "#E7F3E9", Dark: "#222B22"} // winner-row tint
	cCarrot = lipgloss.AdaptiveColor{Light: "#ED7032", Dark: "#ED7032"} // current-step marker

	stTitle    = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	stPick     = lipgloss.NewStyle().Foreground(cPick)
	stSelected = lipgloss.NewStyle().Foreground(cGood)
	stStar     = lipgloss.NewStyle().Foreground(cStar)
	stGood     = lipgloss.NewStyle().Foreground(cGood)
	stWarn     = lipgloss.NewStyle().Foreground(cWarn)
	stErr      = lipgloss.NewStyle().Foreground(cErr)
	stDim      = lipgloss.NewStyle().Foreground(cDim)
	stHead     = lipgloss.NewStyle().Bold(true).Foreground(cDim)
	stHelp     = lipgloss.NewStyle().Foreground(cDim).MarginTop(1)
	stWinRow   = lipgloss.NewStyle().Background(cWinBg)

	stChip = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cDim).
		Foreground(cDim).
		Padding(0, 1)
	stChipAccent = stChip.
			BorderForeground(cStar).
			Foreground(cStar)
)

// scoreStyle grades a score: green ≥ 80, amber ≥ 60, red below.
func scoreStyle(v float64) lipgloss.Style {
	switch {
	case v >= 80:
		return stGood
	case v >= 60:
		return stWarn
	default:
		return stErr
	}
}

// chips renders rounded-border badges side by side. The last chip gets the
// amber accent (mirrors the "config vN" chip in the design).
func chips(labels ...string) string {
	if len(labels) == 0 {
		return ""
	}
	rendered := make([]string, 0, 2*len(labels)-1)
	for i, l := range labels {
		if i > 0 {
			rendered = append(rendered, " ")
		}
		st := stChip
		if i == len(labels)-1 {
			st = stChipAccent
		}
		rendered = append(rendered, st.Render(l))
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, rendered...)
}
