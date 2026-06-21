package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/option-ai/benchy/cli/internal/runner"
	"github.com/option-ai/benchy/cli/internal/score"
)

// RenderRunList shows past runs, newest first: id, size, judge, winner.
func RenderRunList(runs []*runner.RunResult) string {
	var b strings.Builder
	b.WriteString(stTitle.Render("Runs") + "\n")
	wid := 3
	for _, r := range runs {
		wid = max(wid, utf8.RuneCountInString(r.ID))
	}
	for _, r := range runs {
		winner := stDim.Render("—")
		if len(r.Leaderboard) > 0 {
			winner = stStar.Render("★ ") + r.Leaderboard[0].Model + " " +
				scoreStyle(r.Leaderboard[0].Score).Bold(true).Render(fmt.Sprintf("%.1f", r.Leaderboard[0].Score))
		}
		nEvals := map[string]bool{}
		for _, res := range r.Results {
			nEvals[res.Eval] = true
		}
		fmt.Fprintf(&b, "  %s  %s   %s\n",
			pad(r.ID, wid),
			stDim.Render(fmt.Sprintf("%d eval(s) × %d result(s) · judge %s", len(nEvals), len(r.Results), r.Judge)),
			winner)
	}
	b.WriteString(stHelp.Render("benchy results <id> for detail · benchy results compare <a> <b>"))
	return b.String()
}

// RenderAllTime aggregates every run into one all-time leaderboard. Failed
// jobs count as 0 (they were real attempts).
func RenderAllTime(runs []*runner.RunResult) string {
	type agg struct {
		sum  float64
		n    int
		runs map[string]bool
	}
	byModel := map[string]*agg{}
	for _, r := range runs {
		for _, res := range r.Results {
			a := byModel[res.Model]
			if a == nil {
				a = &agg{runs: map[string]bool{}}
				byModel[res.Model] = a
			}
			a.sum += res.Composite
			a.n++
			a.runs[r.ID] = true
		}
	}
	type row struct {
		model string
		score float64
		n     int
		runs  int
	}
	var rows []row
	for m, a := range byModel {
		rows = append(rows, row{m, a.sum / float64(a.n), a.n, len(a.runs)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score == rows[j].score {
			return rows[i].model < rows[j].model
		}
		return rows[i].score > rows[j].score
	})

	var b strings.Builder
	b.WriteString(stTitle.Render("All-time leaderboard") + "\n\n")
	wm := 5
	for _, r := range rows {
		wm = max(wm, utf8.RuneCountInString(r.model))
	}
	for i, r := range rows {
		b.WriteString(leaderRow(i, r.model, wm, r.score,
			fmt.Sprintf("(%d job(s) over %d run(s))", r.n, r.runs)) + "\n")
	}
	if len(rows) == 0 {
		b.WriteString(stDim.Render("  no completed runs yet") + "\n")
	}
	return b.String()
}

// RenderCompare puts two runs side by side per model, with deltas for models
// present in both.
func RenderCompare(a, b *runner.RunResult) string {
	scores := func(r *runner.RunResult) map[string]float64 {
		out := map[string]float64{}
		for _, row := range r.Leaderboard {
			out[row.Model] = row.Score
		}
		return out
	}
	sa, sb := scores(a), scores(b)
	models := map[string]bool{}
	for m := range sa {
		models[m] = true
	}
	for m := range sb {
		models[m] = true
	}
	var names []string
	for m := range models {
		names = append(names, m)
	}
	sort.Strings(names)

	var out strings.Builder
	out.WriteString(stTitle.Render("Compare") + "\n")
	out.WriteString(stDim.Render(fmt.Sprintf("  A = %s   B = %s", a.ID, b.ID)) + "\n\n")
	wm := 5
	for _, m := range names {
		wm = max(wm, utf8.RuneCountInString(m))
	}
	fmt.Fprintf(&out, "  %s  %s  %s  %s\n", pad("model", wm), pad("A", 6), pad("B", 6), "Δ")
	for _, m := range names {
		va, oka := sa[m]
		vb, okb := sb[m]
		cell := func(v float64, ok bool) string {
			if !ok {
				return pad("—", 6)
			}
			return fmt.Sprintf("%6.1f", v)
		}
		delta := stDim.Render("     —")
		if oka && okb {
			d := vb - va
			ds := fmt.Sprintf("%+6.1f", d)
			switch {
			case d > 0:
				delta = stGood.Render(ds)
			case d < 0:
				delta = stErr.Render(ds)
			default:
				delta = stDim.Render(ds)
			}
		}
		fmt.Fprintf(&out, "  %s  %s  %s  %s\n", pad(m, wm), cell(va, oka), cell(vb, okb), delta)
	}
	return out.String()
}

// RenderRunDetail is the full view of one past run, including judge rationales.
func RenderRunDetail(r *runner.RunResult) string {
	var b strings.Builder
	b.WriteString(stTitle.Render("Run "+r.ID) + "\n")
	b.WriteString(stDim.Render(fmt.Sprintf("  started %s · judge %s · config v%d", r.StartedAt, r.Judge, r.ConfigVer)) + "\n")
	b.WriteString(RenderResults(r))
	b.WriteString(stDim.Render("\n  artifacts: "+r.Dir+"/jobs/") + "\n")
	return b.String()
}

var _ = score.Result{}
