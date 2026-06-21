package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// version is stamped at build time:
//
//	go build -ldflags "-X github.com/option-ai/benchy/cli/cmd.version=v0.5.0"
//
// "dev" means a from-source build.
var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the benchy version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("benchy %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
	},
}
