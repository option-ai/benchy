package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScratchWorkspaceCapturesNewFiles verifies the repo-less path: a fresh
// git-initialised workspace plus a diff that surfaces files the agent creates.
func TestScratchWorkspaceCapturesNewFiles(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "scratch")

	if err := scratchWorkspace(ctx, dir); err != nil {
		t.Fatalf("scratchWorkspace: %v", err)
	}
	// Simulate an agent creating a file.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := gitCaptureDiff(ctx, dir)
	if err != nil {
		t.Fatalf("gitCaptureDiff: %v", err)
	}
	if !strings.Contains(diff, "main.go") || !strings.Contains(diff, "package main") {
		t.Fatalf("expected created file in diff, got:\n%s", diff)
	}
}

// TestScratchWorkspaceEmptyDiff verifies a no-op agent yields an empty diff,
// which is the signal to fall back to judging the written answer.
func TestScratchWorkspaceEmptyDiff(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "scratch")
	if err := scratchWorkspace(ctx, dir); err != nil {
		t.Fatalf("scratchWorkspace: %v", err)
	}
	diff, err := gitCaptureDiff(ctx, dir)
	if err != nil {
		t.Fatalf("gitCaptureDiff: %v", err)
	}
	if strings.TrimSpace(diff) != "" {
		t.Fatalf("expected empty diff, got:\n%s", diff)
	}
}
