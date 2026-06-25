package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/snapshot"
)

func TestListCandidatesIncludesCreatedDate(t *testing.T) {
	t.Setenv("BENCHY_HOME", t.TempDir())
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	s := &snapshot.Snapshot{
		Path:    filepath.Join(config.CandidatesDir(), "auto-date.md"),
		Prompts: []string{"fix the date column"},
		Notes:   "<!-- benchy auto-capture\nverdict: weak (score 65)\n-->",
	}
	s.Title = "dated candidate"
	s.Created = "2026-06-25"
	s.Source = "auto"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	cands, err := listCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("candidates = %d, want 1", len(cands))
	}
	if cands[0].created != "2026-06-25" {
		t.Fatalf("created = %q, want 2026-06-25", cands[0].created)
	}
	if line := candidateLine(cands[0]); !strings.Contains(line, "added 2026-06-25") {
		t.Fatalf("candidate line missing date: %q", line)
	}
}

func TestEvalLineIncludesAddedDate(t *testing.T) {
	s := &snapshot.Snapshot{Prompts: []string{"fix the date column"}}
	s.Title = "dated eval"
	s.Created = "2026-06-25"

	line := evalLine(s, "scratch (no repo)", "oneshot")
	if !strings.Contains(line, "added 2026-06-25") {
		t.Fatalf("eval line missing date: %q", line)
	}

	s.Created = ""
	line = evalLine(s, "scratch (no repo)", "oneshot")
	if !strings.Contains(line, "added unknown") {
		t.Fatalf("eval line missing fallback date: %q", line)
	}
}
