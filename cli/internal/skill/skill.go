// Package skill embeds the add-to-benchy skill so `benchy install`/`benchy setup`
// can write it into the user's global Claude Code skills directory.
package skill

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed SKILL.md
var Markdown string

// Name is the skill directory/identifier.
const Name = "add-to-benchy"

// Install writes the skill into ~/.claude/skills/<Name>/SKILL.md and returns
// the path written.
func Install() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".claude", "skills", Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// drop the pre-rebrand skill so both names don't show up in Claude Code
	os.RemoveAll(filepath.Join(home, ".claude", "skills", "add-to-bench"))
	dest := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(dest, []byte(Markdown), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}
