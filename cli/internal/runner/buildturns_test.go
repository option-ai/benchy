package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/snapshot"
)

func TestBuildTurnsInjectsImageRefs(t *testing.T) {
	// A snapshot whose assets dir holds one real image file.
	dir := t.TempDir()
	evalPath := filepath.Join(dir, "task.md")
	assets := strings.TrimSuffix(evalPath, ".md") + ".assets"
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	imgPath := filepath.Join(assets, "img-1.png")
	if err := os.WriteFile(imgPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &snapshot.Snapshot{Prompts: []string{"match the mockup"}, Path: evalPath}
	s.Images = []string{"img-1.png"}

	turns := buildTurns(s, config.Config{DefaultReplay: config.ReplayOneShot}, s.ImagePaths())
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1 (oneshot)", len(turns))
	}
	if !strings.Contains(turns[0], imgPath) {
		t.Errorf("oneshot turn missing image path %q:\n%s", imgPath, turns[0])
	}
	if !strings.Contains(turns[0], "Reference image") {
		t.Errorf("oneshot turn missing reference-image section:\n%s", turns[0])
	}

	// No images => no reference section.
	plain := buildTurns(&snapshot.Snapshot{Prompts: []string{"do a thing"}}, config.Config{DefaultReplay: config.ReplayOneShot}, nil)
	if strings.Contains(plain[0], "Reference image") {
		t.Errorf("unexpected reference-image section when no images:\n%s", plain[0])
	}
}
