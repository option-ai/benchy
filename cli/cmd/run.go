package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/option-ai/benchy/cli/internal/adapter"
	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/report"
	"github.com/option-ai/benchy/cli/internal/runner"
	"github.com/option-ai/benchy/cli/internal/snapshot"
	"github.com/option-ai/benchy/cli/internal/tui"
	"github.com/spf13/cobra"
)

var (
	flagTimeout  time.Duration
	flagJudgeTO  time.Duration
	flagJudgeRef string
	flagEvals    []string
	flagModels   []string
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Select evals + models, run them, and score into a leaderboard",
	Long: `Interactively pick evals and models, or pass --evals/--models/--judge to skip
the pickers entirely (useful for scripting). The judge defaults to the one
chosen during ` + "`benchy setup`" + ` (default_judge in config.json).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		snaps, err := snapshot.LoadAll()
		if err != nil {
			return err
		}
		if len(snaps) == 0 {
			fmt.Println("No evals yet. Capture one with /add-to-benchy inside Claude Code.")
			return nil
		}
		models := enabledModels(cfg)
		if len(models) == 0 {
			fmt.Println("No coding agents found on PATH (claude, codex, cursor-agent, opencode) — or all are skipped; run benchy setup to re-enable.")
			return nil
		}

		// Pickers run as one wizard (breadcrumb header, q/esc goes back a
		// step, selections survive going back) — mirrors the benchy.run demo.
		// Flags skip their step entirely.
		evalItems := make([]tui.Item, len(snaps))
		for i, s := range snaps {
			anchor := "scratch"
			if !s.IsScratch() {
				anchor = fmt.Sprintf("%s@%.8s", s.Repo, s.Commit)
			}
			evalItems[i] = tui.Item{Label: s.Title, Desc: anchor}
		}
		modelItems := make([]tui.Item, len(models))
		for i, m := range models {
			modelItems[i] = tui.Item{Label: m.Ref(), Desc: m.Agent}
		}

		var selEvals []*snapshot.Snapshot
		var selModels []adapter.ModelRef
		var judge adapter.ModelRef

		evalsPicked := len(flagEvals) > 0
		modelsPicked := len(flagModels) > 0
		judgePicked := flagJudgeRef != "" || cfg.DefaultJudge != ""
		if evalsPicked {
			selEvals, err = evalsByName(snaps, flagEvals)
			if err != nil {
				return err
			}
		}
		if modelsPicked {
			for _, m := range flagModels {
				ref, err := adapter.ParseRef(m)
				if err != nil {
					return err
				}
				selModels = append(selModels, ref)
			}
		}
		switch {
		case flagJudgeRef != "":
			judge, err = adapter.ParseRef(flagJudgeRef)
			if err != nil {
				return err
			}
		case cfg.DefaultJudge != "":
			judge, err = adapter.ParseRef(cfg.DefaultJudge)
			if err != nil {
				return fmt.Errorf("config default_judge: %w", err)
			}
		}

		// Pre-select what last run used (config remembers titles/refs), so a
		// repeat run is just enter, enter, enter.
		evalSel := indicesWhere(len(snaps), func(i int) bool {
			return contains(cfg.LastEvals, snaps[i].Title)
		})
		modelSel := indicesWhere(len(models), func(i int) bool {
			return contains(cfg.LastModels, models[i].Ref())
		})
		var judgeSel []int // survive back-navigation
		step := tui.StepEvals
		for step < tui.StepRun {
			switch step {
			case tui.StepEvals:
				if evalsPicked {
					step++
					continue
				}
				ei, _, err := tui.PickStep("Select evals to run", evalItems, true, tui.StepEvals, evalSel, false)
				if err != nil {
					return err
				}
				evalSel = ei
				selEvals = pick(snaps, ei)
				step++
			case tui.StepModels:
				if modelsPicked {
					step++
					continue
				}
				canBack := !evalsPicked
				mi, back, err := tui.PickStep("Select models", modelItems, true, tui.StepModels, modelSel, canBack)
				if err != nil {
					return err
				}
				if back {
					step--
					continue
				}
				modelSel = mi
				selModels = pick(models, mi)
				step++
			case tui.StepJudge:
				if judgePicked {
					step++
					continue
				}
				canBack := !modelsPicked || !evalsPicked
				ji, back, err := tui.PickStep("Select the judge (blind grader — sees only the diff + rubric)", modelItems, false, tui.StepJudge, judgeSel, canBack)
				if err != nil {
					return err
				}
				if back {
					if modelsPicked {
						step = tui.StepEvals
					} else {
						step--
					}
					continue
				}
				judgeSel = ji
				judge = models[ji[0]]
				step++
			}
		}

		// Remember this selection as the next run's default. Best-effort.
		cfg.LastEvals = cfg.LastEvals[:0]
		for _, e := range selEvals {
			cfg.LastEvals = append(cfg.LastEvals, e.Title)
		}
		cfg.LastModels = cfg.LastModels[:0]
		for _, m := range selModels {
			cfg.LastModels = append(cfg.LastModels, m.Ref())
		}
		_ = config.Save(cfg)

		// 4. run with a live progress view and a cancellable context: if the
		// user quits the view, the agents must die too (they cost money).
		fmt.Printf("\nRunning %d eval(s) × %d model(s) · judge %s %s\n\n",
			len(selEvals), len(selModels), judge.Ref(), dimNote(flagJudgeRef, cfg))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		events := make(chan runner.Event, 256)
		resCh := make(chan *runner.RunResult, 1)
		errCh := make(chan error, 1)
		go func() {
			r, err := runner.Run(ctx, runner.Options{
				Evals:       selEvals,
				Models:      selModels,
				Judge:       judge,
				Cfg:         cfg,
				AgentBudget: adapter.Budget{Timeout: flagTimeout},
				JudgeTO:     flagJudgeTO,
				Now:         time.Now(),
				Events:      events,
			})
			close(events)
			if err != nil {
				errCh <- err
			} else {
				resCh <- r
			}
		}()

		evalLabels := make([]string, len(selEvals))
		for i, e := range selEvals {
			evalLabels[i] = e.Title
		}
		modelLabels := make([]string, len(selModels))
		for i, m := range selModels {
			modelLabels[i] = m.Ref()
		}
		interrupted := false
		if fi, _ := os.Stdout.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
			var uiErr error
			interrupted, uiErr = tui.RunProgress(evalLabels, modelLabels, events)
			if uiErr != nil {
				// The view failing must not kill paid agent runs; fall back to
				// plain progress on whatever events remain.
				fmt.Printf("(live view unavailable: %v)\n", uiErr)
				tui.RunProgressPlain(evalLabels, modelLabels, events)
			}
		} else {
			tui.RunProgressPlain(evalLabels, modelLabels, events)
		}
		if interrupted {
			fmt.Println("\nInterrupted — stopping agents…")
			cancel()
		}
		// Drain any events still buffered/being emitted so the runner can
		// finish (emit blocks once the buffer fills if no one reads).
		go func() {
			for range events {
			}
		}()

		select {
		case err := <-errCh:
			return err
		case res := <-resCh:
			if interrupted {
				fmt.Println("Run cancelled; partial results below.")
			}
			fmt.Println("\n" + tui.Crumbs(tui.StepResults))
			fmt.Print(tui.RenderResults(res))
			fmt.Printf("\nFull results: %s/run.json\n", res.Dir)
			if cfg.ReportResults && !interrupted {
				if err := report.Send(context.Background(), res.Judge, res.Leaderboard); err != nil {
					fmt.Printf("Could not report to the global leaderboard: %v\n", err)
				} else {
					fmt.Println("Reported scores to the global leaderboard (benchy.run).")
				}
			}
		}
		return nil
	},
}

func dimNote(judgeFlag string, cfg config.Config) string {
	if judgeFlag != "" {
		return "(--judge)"
	}
	if cfg.DefaultJudge != "" {
		return "(config default; override with --judge)"
	}
	return ""
}

// evalsByName resolves --evals values against titles and slugs.
func evalsByName(snaps []*snapshot.Snapshot, names []string) ([]*snapshot.Snapshot, error) {
	var out []*snapshot.Snapshot
	for _, n := range names {
		found := false
		for _, s := range snaps {
			if s.Title == n || snapshot.Slug(s.Title) == snapshot.Slug(n) {
				out = append(out, s)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("no eval named %q (have: %s)", n, evalNames(snaps))
		}
	}
	return out, nil
}

func evalNames(snaps []*snapshot.Snapshot) string {
	var names []string
	for _, s := range snaps {
		names = append(names, s.Title)
	}
	return strings.Join(names, ", ")
}

func init() {
	runCmd.Flags().DurationVar(&flagTimeout, "timeout", 20*time.Minute, "whole-run timeout per agent job (all turns)")
	runCmd.Flags().DurationVar(&flagJudgeTO, "judge-timeout", 5*time.Minute, "judge invocation timeout")
	runCmd.Flags().StringVar(&flagJudgeRef, "judge", "", "judge model ref (agent:model); overrides config default_judge")
	runCmd.Flags().StringSliceVar(&flagEvals, "evals", nil, "eval titles to run (skips the picker)")
	runCmd.Flags().StringSliceVar(&flagModels, "models", nil, "model refs to run, agent:model (skips the picker)")
}

func pick[T any](all []T, idxs []int) []T {
	out := make([]T, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, all[i])
	}
	return out
}

// enabledModels lists selectable models, honoring config model overrides and
// the agents the user opted out of during setup.
func enabledModels(cfg config.Config) []adapter.ModelRef {
	all := adapter.AvailableModelsWith(cfg.Models)
	out := all[:0]
	for _, m := range all {
		if !cfg.AgentDisabled(m.Agent) {
			out = append(out, m)
		}
	}
	return out
}

func indicesWhere(n int, keep func(int) bool) []int {
	var out []int
	for i := 0; i < n; i++ {
		if keep(i) {
			out = append(out, i)
		}
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
