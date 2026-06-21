// Package cmd wires the benchy CLI: run, list, auth, install.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var root = &cobra.Command{
	Use:   "benchy",
	Short: "A personal benchmark for coding agents, seeded from your real sessions",
	Long: `benchy replays the prompts from your real coding sessions against multiple
agents on the exact repo state you captured, then a blind judge scores each
diff into a single composite number so you can compare models head-to-head.

Capture evals with the /add-to-benchy skill inside Claude Code, then run them here.`,
}

// Execute runs the root command.
func Execute() {
	err := root.Execute()
	maybeUpdateHint()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	root.Version = version
	candidatesCmd.AddCommand(promoteCmd)
	root.AddCommand(setupCmd, listCmd, modelsCmd, runCmd, resultsCmd, installCmd, versionCmd, updateCmd, captureCmd, candidatesCmd)
}
