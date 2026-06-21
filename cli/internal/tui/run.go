package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/option-ai/benchy/cli/internal/runner"
	"github.com/option-ai/benchy/cli/internal/score"
)

type rowKey struct{ eval, model string }

type eventMsg runner.Event
type finishedMsg struct{}

type progressModel struct {
	order       []rowKey
	stage       map[rowKey]runner.Stage
	errs        map[rowKey]string
	wEval       int
	wModel      int
	width       int // terminal width; rows are clipped to it so they never wrap
	events      <-chan runner.Event
	spin        spinner.Model
	done        bool
	interrupted bool
}

func waitEvent(ch <-chan runner.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return finishedMsg{}
		}
		return eventMsg(e)
	}
}

func (m progressModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, waitEvent(m.events))
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		k := rowKey{msg.Eval, msg.Model}
		m.stage[k] = msg.Stage
		if msg.Err != nil {
			m.errs[k] = msg.Err.Error()
		}
		return m, waitEvent(m.events)
	case finishedMsg:
		m.done = true
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.interrupted = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m progressModel) View() string {
	var b strings.Builder
	b.WriteString(Crumbs(StepRun) + "\n\n")
	for _, k := range m.order {
		st := m.stage[k]
		var status string
		switch st {
		case runner.StageDone:
			status = stGood.Render("✓ " + st.Label())
		case runner.StageError:
			msg := m.errs[k]
			if len(msg) > 48 {
				msg = msg[:48] + "…"
			}
			status = stErr.Render("✗ " + msg)
		case runner.StageQueued, "":
			status = stDim.Render("· queued")
		default:
			status = m.spin.View() + stDim.Render(st.Label())
		}
		line := fmt.Sprintf("  %s  %s  %s",
			pad(k.eval, m.wEval), pad(k.model, m.wModel), status)
		b.WriteString(clip(line, m.width) + "\n")
	}
	b.WriteString("\n" + stTitle.Render("◐") + stDim.Render(" scoring with blind judge — never sees the model id") + "\n")
	return b.String()
}

// RunProgress renders a live, aligned status view that updates in place as the
// runner emits events, until the events channel closes. It returns
// interrupted=true if the user hit ctrl+c before the run finished — the caller
// must then cancel the run and drain the events channel.
func RunProgress(evals, models []string, events <-chan runner.Event) (interrupted bool, err error) {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = stPick

	m := progressModel{
		stage:  map[rowKey]runner.Stage{},
		errs:   map[rowKey]string{},
		events: events,
		spin:   sp,
		wEval:  width(evals),
		wModel: width(models),
	}
	for _, e := range evals {
		for _, md := range models {
			k := rowKey{e, md}
			m.order = append(m.order, k)
			m.stage[k] = runner.StageQueued
		}
	}
	// Alt screen: the inline renderer corrupts on terminal resize (wrapped or
	// stale lines double up); the alt screen repaints fully every frame and
	// restores the shell content on exit. Final results print after.
	out, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return false, err
	}
	return out.(progressModel).interrupted, nil
}

// clip truncates a rendered line to the terminal width, ANSI-aware so styled
// text is never cut mid-escape.
func clip(s string, w int) string {
	if w <= 0 {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// RenderResults produces the final leaderboard + per-eval breakdown as aligned,
// theme-adaptive tables.
func RenderResults(res *runner.RunResult) string {
	var b strings.Builder

	b.WriteString("\n" + stTitle.Render("Leaderboard") + "\n\n")
	mw := 5
	for _, row := range res.Leaderboard {
		mw = max(mw, utf8.RuneCountInString(row.Model))
	}
	for i, row := range res.Leaderboard {
		b.WriteString(leaderRow(i, row.Model, mw, row.Score,
			fmt.Sprintf("(%d run%s)", row.Runs, plural(row.Runs))) + "\n")
	}

	b.WriteString("\n" + stTitle.Render("Per-eval breakdown") + "\n\n")
	ew, mw2 := 4, 5
	for _, r := range res.Results {
		ew = max(ew, utf8.RuneCountInString(r.Eval))
		mw2 = max(mw2, utf8.RuneCountInString(r.Model))
	}
	for _, r := range res.Results {
		fmt.Fprintf(&b, "  %s  %s  %s\n",
			pad(r.Eval, ew), stDim.Render(pad(r.Model, mw2)), evalOutcome(r))
	}

	if rat := renderRationales(res); rat != "" {
		b.WriteString(rat)
	}

	b.WriteString("\n" + chips("judge "+res.Judge, fmt.Sprintf("config v%d", res.ConfigVer)) + "\n")
	b.WriteString(stHelp.Render("run again: benchy run · compare: benchy results compare") + "\n")
	return b.String()
}

// renderRationales lists the judge's reasoning per job, as in the demo's
// results screen. Empty when no job carries a rationale.
func renderRationales(res *runner.RunResult) string {
	var b strings.Builder
	for _, r := range res.Results {
		if r.Rationale == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("\n" + stTitle.Render("Rationales") + "\n\n")
		}
		fmt.Fprintf(&b, "  %s\n", stHead.Render(r.Eval+" · "+r.Model))
		fmt.Fprintf(&b, "    %s\n", r.Rationale)
	}
	return b.String()
}

// leaderRow renders one leaderboard line: dim rank, ★ on the winner, model
// name, graded score, dim run count. The winner row gets a subtle tint —
// applied per segment, since each inner color reset would clear a
// whole-line background.
func leaderRow(i int, model string, mw int, score float64, note string) string {
	bg := lipgloss.NewStyle()
	star := " "
	if i == 0 {
		bg = stWinRow
		star = stStar.Inherit(stWinRow).Render("★")
	}
	return fmt.Sprintf("%s%s%s%s%s%s%s%s%s%s",
		bg.Render("  "),
		stDim.Inherit(bg).Render(fmt.Sprintf("%d.", i+1)),
		bg.Render(" "), star, bg.Render(" "),
		bg.Render(pad(model, mw)+"   "),
		scoreStyle(score).Bold(true).Inherit(bg).Render(fmt.Sprintf("%5.1f", score)),
		bg.Render("  "),
		stDim.Inherit(bg).Render(note),
		bg.Render(" "))
}

// evalOutcome renders the right-hand cell of a per-eval row:
// ✓ 91.2 · judge 92 · gate 1.00 in green, or the failure in red.
func evalOutcome(r score.Result) string {
	if r.Err != "" {
		msg := r.Err
		if len(msg) > 48 {
			msg = msg[:48] + "…"
		}
		return stErr.Render(fmt.Sprintf("✗ %s — gate capped at %.2f", msg, r.GateFactor))
	}
	gate := stGood.Render(fmt.Sprintf("gate %.2f", r.GateFactor))
	if r.GateFactor < 1 {
		gate = stWarn.Render(fmt.Sprintf("gate %.2f", r.GateFactor))
	}
	return fmt.Sprintf("%s %s %s %s %s %s",
		stGood.Render("✓"),
		scoreStyle(r.Composite).Bold(true).Render(fmt.Sprintf("%.1f", r.Composite)),
		stDim.Render("·"),
		stGood.Render(fmt.Sprintf("judge %.0f", r.JudgeOverall)),
		stDim.Render("·"),
		gate)
}

// pad right-pads s to n visible runes.
func pad(s string, n int) string {
	d := n - utf8.RuneCountInString(s)
	if d <= 0 {
		return s
	}
	return s + strings.Repeat(" ", d)
}

func width(ss []string) int {
	w := 0
	for _, s := range ss {
		w = max(w, utf8.RuneCountInString(s))
	}
	return w
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// RunProgressPlain consumes events and prints one aligned line per update —
// the fallback when stdout isn't a TTY (scripts, CI, piped output), where the
// live in-place view can't run.
func RunProgressPlain(evals, models []string, events <-chan runner.Event) {
	we, wm := width(evals), width(models)
	for e := range events {
		status := e.Stage.Label()
		if e.Stage == runner.StageError && e.Err != nil {
			status = "error: " + e.Err.Error()
		}
		fmt.Printf("  %s  %s  %s\n", pad(e.Eval, we), pad(e.Model, wm), status)
	}
}
