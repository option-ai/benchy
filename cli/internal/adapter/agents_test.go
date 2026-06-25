package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryIncludesSupportedHarnesses(t *testing.T) {
	for _, id := range []string{"claude-code", "codex", "cursor-agent", "opencode"} {
		if Get(id) == nil {
			t.Fatalf("Get(%q) = nil", id)
		}
	}
}

func TestCodexRunUsesExecResumeAndOutputFile(t *testing.T) {
	log := installFakeCLI(t, "codex", `#!/bin/sh
printf '%s\n' "$*" >> "$BENCHY_FAKE_LOG"
out=""
last=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--output-last-message" ]; then
    out="$arg"
  fi
  last="$arg"
  prev="$arg"
done
if [ -n "$out" ]; then
  printf 'codex response to %s\n' "$last" > "$out"
fi
printf 'codex banner that should be ignored\n'
`)

	responses, err := (&codex{}).Run(context.Background(), t.TempDir(), []string{"first-turn", "second-turn"}, "gpt-5.1-codex", Budget{Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"codex response to first-turn", "codex response to second-turn"}; strings.Join(responses, "\n") != strings.Join(want, "\n") {
		t.Fatalf("responses = %#v, want %#v", responses, want)
	}

	lines := readLines(t, log)
	if len(lines) != 2 {
		t.Fatalf("logged commands = %d, want 2: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "exec --model gpt-5.1-codex ") {
		t.Errorf("first codex command = %q", lines[0])
	}
	for _, want := range []string{"--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "--output-last-message", "first-turn"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("first codex command missing %q: %q", want, lines[0])
		}
	}
	if !strings.HasPrefix(lines[1], "exec resume --last --model gpt-5.1-codex ") {
		t.Errorf("second codex command = %q", lines[1])
	}
	if !strings.Contains(lines[1], "second-turn") {
		t.Errorf("second codex command missing prompt: %q", lines[1])
	}
}

func TestOpenCodeRunUsesUnattendedModeAndContinue(t *testing.T) {
	log := installFakeCLI(t, "opencode", `#!/bin/sh
printf '%s\n' "$*" >> "$BENCHY_FAKE_LOG"
last=""
for arg in "$@"; do
  last="$arg"
done
printf '\033[31mopencode response to %s\033[0m\n' "$last"
`)

	dir := t.TempDir()
	responses, err := (&openCode{}).Run(context.Background(), dir, []string{"first-turn", "second-turn"}, "openai/gpt-5.1-codex", Budget{Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"opencode response to first-turn", "opencode response to second-turn"}; strings.Join(responses, "\n") != strings.Join(want, "\n") {
		t.Fatalf("responses = %#v, want %#v", responses, want)
	}

	lines := readLines(t, log)
	if len(lines) != 2 {
		t.Fatalf("logged commands = %d, want 2: %v", len(lines), lines)
	}
	for _, want := range []string{"run --model openai/gpt-5.1-codex", "--dangerously-skip-permissions", "--dir " + dir, "first-turn"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("first opencode command missing %q: %q", want, lines[0])
		}
	}
	for _, want := range []string{"--continue", "--dangerously-skip-permissions", "second-turn"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("second opencode command missing %q: %q", want, lines[1])
		}
	}
}

func installFakeCLI(t *testing.T, name, body string) string {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), name+".log")
	t.Setenv("BENCHY_FAKE_LOG", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}
