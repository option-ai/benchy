package cmd

import (
	"fmt"

	"github.com/option-ai/benchy/cli/internal/runner"
	"github.com/option-ai/benchy/cli/internal/tui"
	"github.com/spf13/cobra"
)

var resultsCmd = &cobra.Command{
	Use:   "results [run-id]",
	Short: "Browse past runs: all-time leaderboard, run detail, comparisons",
	Long: `With no arguments: the all-time leaderboard (every model's mean composite
across all persisted runs) plus the run history.
With a run id (or unique prefix): that run's full detail, including rationales.
Use "results compare <a> <b>" to diff two runs model-by-model.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			r, err := runner.FindRun(args[0])
			if err != nil {
				return err
			}
			fmt.Println(tui.RenderRunDetail(r))
			return nil
		}
		runs, err := runner.ListRuns()
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			fmt.Println("No runs yet. `benchy run` to create one.")
			return nil
		}
		fmt.Println(tui.RenderAllTime(runs))
		fmt.Println(tui.RenderRunList(runs))
		return nil
	},
}

var resultsCompareCmd = &cobra.Command{
	Use:   "compare <run-a> <run-b>",
	Short: "Compare two runs model-by-model with deltas",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := runner.FindRun(args[0])
		if err != nil {
			return err
		}
		b, err := runner.FindRun(args[1])
		if err != nil {
			return err
		}
		fmt.Println(tui.RenderCompare(a, b))
		return nil
	},
}

func init() {
	resultsCmd.AddCommand(resultsCompareCmd)
}
