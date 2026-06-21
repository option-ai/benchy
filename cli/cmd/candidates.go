package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/snapshot"
	"github.com/spf13/cobra"
)

var candidatesCmd = &cobra.Command{
	Use:   "candidates",
	Short: "List auto-captured candidate evals awaiting review",
	Long: `Prospective capture writes sessions that pass the portability filter to a
review queue (` + "`~/.config/benchy/candidates`" + `) instead of straight into your evals.
Review them here, then promote the good ones into the run set with
` + "`benchy candidates promote <name>`" + `.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cands, err := listCandidates()
		if err != nil {
			return err
		}
		if len(cands) == 0 {
			fmt.Println("no candidates yet — they appear here as you finish Claude Code sessions.")
			fmt.Printf("(queue: %s)\n", config.CandidatesDir())
			return nil
		}
		for _, c := range cands {
			fmt.Printf("• %-40s %s\n", c.name, c.verdict)
			fmt.Printf("  %s\n", c.title)
		}
		fmt.Printf("\nPromote one with: benchy candidates promote <name>\n")
		return nil
	},
}

var promoteCmd = &cobra.Command{
	Use:   "promote <name>",
	Short: "Move a reviewed candidate into the run set",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !strings.HasSuffix(name, ".md") {
			name += ".md"
		}
		src := filepath.Join(config.CandidatesDir(), name)
		s, err := snapshot.Load(src)
		if err != nil {
			return fmt.Errorf("load candidate: %w", err)
		}
		srcAssets := s.AssetsDir() // resolved from the candidate path, before re-homing
		// Re-home under the snapshots dir with a readable slug, drop the Notes.
		s.Notes = ""
		s.Path = filepath.Join(config.SnapshotsDir(), snapshot.Slug(s.Title)+".md")
		if dstAssets := s.AssetsDir(); srcAssets != "" && dstAssets != "" {
			if _, statErr := os.Stat(srcAssets); statErr == nil {
				os.RemoveAll(dstAssets)
				if err := os.Rename(srcAssets, dstAssets); err != nil {
					return fmt.Errorf("move assets: %w", err)
				}
			}
		}
		if err := s.Save(); err != nil {
			return err
		}
		_ = os.Remove(src)
		fmt.Printf("✓ promoted → %s\n", s.Path)
		fmt.Println("Run it with: benchy run")
		return nil
	},
}

type candidate struct {
	name    string
	title   string
	verdict string
}

func listCandidates() ([]candidate, error) {
	dir := config.CandidatesDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		s, err := snapshot.Load(path)
		if err != nil {
			continue
		}
		out = append(out, candidate{
			name:    strings.TrimSuffix(e.Name(), ".md"),
			title:   s.Title,
			verdict: verdictLine(path),
		})
	}
	return out, nil
}

// verdictLine pulls the "verdict: …" line out of a candidate's notes block.
func verdictLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); strings.HasPrefix(t, "verdict:") {
			return t
		}
	}
	return ""
}
