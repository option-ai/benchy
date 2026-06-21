package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/option-ai/benchy/cli/internal/snapshot"
)

// DetectGates infers build/test/lint commands from the files in repoDir, mirroring
// the heuristics in the add-to-benchy skill. Unknown gates are left empty.
func DetectGates(repoDir string) snapshot.Gates {
	var g snapshot.Gates
	switch {
	case exists(repoDir, "package.json"):
		g = nodeGates(repoDir)
	case exists(repoDir, "go.mod"):
		g = snapshot.Gates{Build: "go build ./...", Test: "go test ./...", Lint: "go vet ./..."}
	case exists(repoDir, "Cargo.toml"):
		g = snapshot.Gates{Build: "cargo build", Test: "cargo test", Lint: "cargo clippy"}
	case exists(repoDir, "pyproject.toml") || exists(repoDir, "setup.py") || exists(repoDir, "pytest.ini"):
		g = snapshot.Gates{Test: "pytest"}
		if exists(repoDir, "ruff.toml") || exists(repoDir, ".ruff.toml") {
			g.Lint = "ruff check"
		}
	}
	return g
}

// nodeGates reads package.json scripts and maps the conventional ones. Falls back
// to a plain runner guess when a script name is present.
func nodeGates(repoDir string) snapshot.Gates {
	b, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return snapshot.Gates{}
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return snapshot.Gates{}
	}
	runner := "npm run"
	if exists(repoDir, "bun.lockb") || exists(repoDir, "bun.lock") {
		runner = "bun run"
	} else if exists(repoDir, "pnpm-lock.yaml") {
		runner = "pnpm"
	} else if exists(repoDir, "yarn.lock") {
		runner = "yarn"
	}
	var g snapshot.Gates
	if has(pkg.Scripts, "build") {
		g.Build = runner + " build"
	}
	if has(pkg.Scripts, "test") {
		g.Test = runner + " test"
	}
	for _, name := range []string{"lint", "check", "typecheck", "check-types"} {
		if has(pkg.Scripts, name) {
			g.Lint = runner + " " + name
			break
		}
	}
	return g
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func has(m map[string]string, k string) bool { _, ok := m[k]; return ok }
