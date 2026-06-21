package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallMergesAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing settings with an unrelated key and a user's own Stop hook.
	seed := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo user-hook"}]}]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Install("/usr/local/bin/benchy"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := Install("/usr/local/bin/benchy"); err != nil { // idempotency
		t.Fatalf("Install (2nd): %v", err)
	}
	if !Installed() {
		t.Fatal("Installed() = false after install")
	}

	b, err := os.ReadFile(filepath.Join(claude, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, b)
	}
	// Unrelated key preserved.
	if s["model"] != "opus" {
		t.Errorf("model key clobbered: %v", s["model"])
	}
	// User's own Stop hook preserved.
	if !strings.Contains(string(b), "echo user-hook") {
		t.Errorf("user hook clobbered:\n%s", b)
	}
	// No duplicates across two installs: one start, one Stop end, one forced
	// SessionEnd end (the latter also contains the substring "capture end").
	if got := strings.Count(string(b), "capture start"); got != 1 {
		t.Errorf("capture start count = %d, want 1\n%s", got, b)
	}
	if got := strings.Count(string(b), "capture end"); got != 2 {
		t.Errorf("capture end count = %d, want 2 (Stop + SessionEnd)\n%s", got, b)
	}
	if got := strings.Count(string(b), "capture end --force"); got != 1 {
		t.Errorf("forced capture end count = %d, want 1\n%s", got, b)
	}
	// All three events created.
	hooks := s["hooks"].(map[string]any)
	for _, ev := range []string{"SessionStart", "Stop", "SessionEnd"} {
		if _, ok := hooks[ev]; !ok {
			t.Errorf("%s not added:\n%s", ev, b)
		}
	}
}
