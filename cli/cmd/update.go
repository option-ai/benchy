package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/skill"
	"github.com/spf13/cobra"
)

const (
	versionURL = "https://benchy.run/api/version"
	dlURLBase  = "https://benchy.run/dl/"
)

var flagUpdateForce bool

var updateCmd = &cobra.Command{
	Use:          "update",
	Short:        "Update benchy to the latest release (binary + skill)",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		latest, err := latestVersion(10 * time.Second)
		if err != nil {
			return fmt.Errorf("could not check the latest version: %w", err)
		}
		if latest == version && !flagUpdateForce {
			fmt.Printf("benchy %s is already the latest version.\n", version)
			return nil
		}

		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return err
		}

		name := "benchy-" + runtime.GOOS + "-" + runtime.GOARCH
		fmt.Printf("Updating %s → %s (%s)...\n", version, latest, name)
		resp, err := http.Get(dlURLBase + name)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("download failed: %s returned %s", dlURLBase+name, resp.Status)
		}

		// Write next to the current binary, then rename over it — atomic on
		// the same filesystem, and the running process keeps its old inode.
		tmp, err := os.CreateTemp(filepath.Dir(exe), ".benchy-update-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, resp.Body); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), exe); err != nil {
			return err
		}
		fmt.Printf("✓ binary   %s\n", exe)

		// Refresh the embedded artifacts the new binary ships.
		if dest, err := skill.Install(); err == nil {
			fmt.Printf("✓ skill    %s\n", dest)
		}
		_ = config.EnsureDirs()
		fmt.Printf("benchy %s installed.\n", latest)
		return nil
	},
}

func latestVersion(timeout time.Duration) (string, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(versionURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %s", versionURL, resp.Status)
	}
	var v struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.Version == "" {
		return "", fmt.Errorf("empty version from %s", versionURL)
	}
	return v.Version, nil
}

// maybeUpdateHint prints a one-line nudge when a newer release exists. Quiet
// by design: at most once a day, only on a TTY, 800ms budget, never an error.
func maybeUpdateHint() {
	if version == "dev" {
		return
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "update", "version", "--version", "-v":
			return // these already told the user where they stand
		}
	}
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	stamp := filepath.Join(config.CacheDir(), "last-update-check")
	if fi, err := os.Stat(stamp); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		return
	}
	_ = os.MkdirAll(filepath.Dir(stamp), 0o755)
	_ = os.WriteFile(stamp, []byte(time.Now().Format(time.RFC3339)), 0o644)
	latest, err := latestVersion(800 * time.Millisecond)
	if err == nil && latest != version {
		fmt.Fprintf(os.Stderr, "\nbenchy %s is available (you have %s) — run `benchy update`\n", latest, version)
	}
}

func init() {
	updateCmd.Flags().BoolVar(&flagUpdateForce, "force", false, "reinstall even if already on the latest version")
}
