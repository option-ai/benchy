package cmd

import (
	"fmt"

	"github.com/option-ai/benchy/cli/internal/adapter"
	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/spf13/cobra"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List the agents benchy detects and the models it will offer",
	Long: `Shows every installed agent and the model ids benchy will present in ` + "`benchy run`" + `,
after applying any per-agent overrides from config.json ("models" map). Use this
to confirm a newly-released model shows up; if it doesn't, add it under "models"
in config.json (path shown below).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		for _, a := range adapter.All() {
			if !a.Available() {
				fmt.Printf("✗ %-13s not installed\n", a.ID())
				continue
			}
			models := a.Models()
			src := "built-in"
			if ov, ok := cfg.Models[a.ID()]; ok && len(ov) > 0 {
				models, src = ov, "config override"
			}
			fmt.Printf("✓ %-13s (%s)\n", a.ID(), src)
			for _, m := range models {
				fmt.Printf("    %s:%s\n", a.ID(), m)
			}
		}
		fmt.Println("\nOverride any list by adding to \"models\" in " + config.Dir() + "/config.json, e.g.")
		fmt.Println(`  "models": { "claude-code": ["claude-fable-5", "claude-opus-4-8"] }`)
		return nil
	},
}
