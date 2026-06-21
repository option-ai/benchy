package runner

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitClone must fall back to the captured source_path when the remote is
// unreachable (private repo, no auth) — Gustavo's case.
func TestGitCloneFallsBackToSourcePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	src := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "--quiet", "-m", "x"},
	} {
		if _, err := git(ctx, src, args...); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	dest := filepath.Join(t.TempDir(), "clone")
	// 127.0.0.1:9 (discard port) refuses instantly — no network dependency
	if err := gitClone(ctx, "https://127.0.0.1:9/own/nope.git", src, dest); err != nil {
		t.Fatalf("expected local fallback to succeed, got: %v", err)
	}
}

func TestGitCloneErrorMentionsAuthHelp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dest := filepath.Join(t.TempDir(), "clone")
	err := gitClone(context.Background(), "https://127.0.0.1:9/own/nope.git", "", dest)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("error should tell the user how to fix auth, got: %v", err)
	}
}
