package cmd

import (
	"fmt"
	"os"

	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/hook"
	"github.com/option-ai/benchy/cli/internal/skill"
	"github.com/spf13/cobra"
)

var noCaptureHooks bool

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the /add-to-benchy skill globally and set up config dirs",
	Long: `Non-interactive setup. For a guided walkthrough (agent logins + judge), use ` + "`benchy setup`" + `.

Also installs prospective-capture hooks into ~/.claude/settings.json: when a
Claude Code session ends, the session is filtered for portability and, if it
passes, written as a candidate eval to review (no skill call needed). Pass
--no-capture-hooks to skip.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := config.Load(); err != nil { // ensures dirs + writes default config.json
			return err
		}
		dest, err := skill.Install()
		if err != nil {
			return err
		}
		fmt.Printf("✓ installed skill   %s\n", dest)
		fmt.Printf("✓ config ready      %s\n", config.Dir())

		if !noCaptureHooks {
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			settings, err := hook.Install(bin)
			if err != nil {
				return fmt.Errorf("install capture hooks: %w", err)
			}
			fmt.Printf("✓ capture hooks     %s\n", settings)
			fmt.Printf("  candidates queue  %s\n", config.CandidatesDir())
		}

		fmt.Println("\nNext: `benchy setup` to check agent logins and pick a judge, or /add-to-benchy inside Claude Code.")
		return nil
	},
}

func init() {
	installCmd.Flags().BoolVar(&noCaptureHooks, "no-capture-hooks", false, "skip installing the prospective-capture hooks into ~/.claude/settings.json")
}
