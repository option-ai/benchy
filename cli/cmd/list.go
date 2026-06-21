package cmd

import (
	"fmt"

	"github.com/option-ai/benchy/cli/internal/snapshot"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List the evals in your bench",
	RunE: func(cmd *cobra.Command, args []string) error {
		snaps, err := snapshot.LoadAll()
		if err != nil {
			return err
		}
		if len(snaps) == 0 {
			fmt.Println("No evals yet. Capture one with /add-to-benchy inside Claude Code.")
			return nil
		}
		for _, s := range snaps {
			anchor := "scratch (no repo)"
			if !s.IsScratch() {
				anchor = fmt.Sprintf("%s@%.8s", s.Repo, s.Commit)
			}
			replay := string(s.Replay)
			if replay == "" {
				replay = "default"
			}
			fmt.Printf("• %-28s %-40s (%d prompt(s), replay=%s)\n",
				s.Title, anchor, len(s.Prompts), replay)
			if s.Feedback != "" {
				fmt.Printf("    rubric: %s\n", s.Feedback)
			}
		}
		return nil
	},
}
