// Package hook installs the prospective-capture hooks into the user's global
// Claude Code settings (~/.claude/settings.json): a SessionStart hook that
// records the base commit, and a Stop hook that captures the finished session as
// a benchy candidate. Installation merges into existing settings idempotently.
package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// isCaptureCmd reports whether a hook command is one of ours. We key off the
// "capture <phase>" subcommand rather than the binary path, which is quoted and
// absolute (so a fixed "benchy capture" substring wouldn't match).
func isCaptureCmd(cmd string) bool {
	return strings.Contains(cmd, "capture start") || strings.Contains(cmd, "capture end")
}

// events maps a Claude Code hook event to the `capture` arguments it runs.
// Stop offloads to a detached worker (returns instantly); SessionEnd forces a
// final synchronous capture so the last turn is never missed.
var events = map[string]string{
	"SessionStart": "start",
	"Stop":         "end",
	"SessionEnd":   "end --force",
}

// SettingsPath returns the global Claude Code settings file path.
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// Install merges the capture hooks into settings.json, pointing at binPath.
// Returns the settings path. Idempotent: re-running replaces our prior entries.
func Install(binPath string) (string, error) {
	path, err := SettingsPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	settings := map[string]any{}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &settings); err != nil {
			return "", fmt.Errorf("parse %s: %w", path, err)
		}
	}
	hooks := asMap(settings["hooks"])
	for event, suffix := range events {
		cmd := fmt.Sprintf("%q capture %s", binPath, suffix)
		hooks[event] = withCaptureGroup(hooks[event], cmd)
	}
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// withCaptureGroup returns the event's hook-group list with our command present
// exactly once: any existing benchy-capture group is dropped, then ours appended.
func withCaptureGroup(existing any, command string) []any {
	kept := []any{}
	for _, g := range asSlice(existing) {
		if !groupHasMarker(g) {
			kept = append(kept, g)
		}
	}
	group := map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	}
	return append(kept, group)
}

// groupHasMarker reports whether a hook group contains a benchy-capture command.
func groupHasMarker(group any) bool {
	gm := asMap(group)
	for _, h := range asSlice(gm["hooks"]) {
		hm := asMap(h)
		if cmd, _ := hm["command"].(string); isCaptureCmd(cmd) {
			return true
		}
	}
	return false
}

// Installed reports whether capture hooks are present in settings.json.
func Installed() bool {
	path, err := SettingsPath()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return isCaptureCmd(string(b))
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
