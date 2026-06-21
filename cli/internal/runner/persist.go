package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/snapshot"
)

// persist writes the aggregate run result as run.json. Per-job diff/output
// artifacts are written separately by saveArtifacts as each job completes.
func persist(runDir string, r *RunResult) error {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	return os.WriteFile(filepath.Join(runDir, "run.json"), b, 0o644)
}

// LoadRun reads a persisted run.json.
func LoadRun(path string) (*RunResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r RunResult
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// saveArtifacts persists a job's diff and replay transcript under
// runDir/jobs/<eval>__<model>/, so results stay inspectable after the working
// trees are cleaned up. Best-effort: scoring proceeds regardless.
func saveArtifacts(runDir, eval, model, diff, transcript string) {
	dir := filepath.Join(runDir, "jobs", snapshot.Slug(eval)+"__"+snapshot.Slug(model))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if diff != "" {
		_ = os.WriteFile(filepath.Join(dir, "diff.patch"), []byte(diff), 0o644)
	}
	if transcript != "" {
		_ = os.WriteFile(filepath.Join(dir, "transcript.txt"), []byte(transcript), 0o644)
	}
}

// ListRuns loads every persisted run, newest first.
func ListRuns() ([]*RunResult, error) {
	entries, err := os.ReadDir(config.RunsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*RunResult
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		r, err := LoadRun(filepath.Join(config.RunsDir(), e.Name(), "run.json"))
		if err != nil {
			continue // incomplete/cancelled run dirs are skipped, not fatal
		}
		r.Dir = filepath.Join(config.RunsDir(), e.Name())
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// FindRun loads one run by ID (or unique ID prefix).
func FindRun(id string) (*RunResult, error) {
	runs, err := ListRuns()
	if err != nil {
		return nil, err
	}
	var match *RunResult
	for _, r := range runs {
		if r.ID == id {
			return r, nil
		}
		if len(id) >= 4 && len(r.ID) >= len(id) && r.ID[:len(id)] == id {
			if match != nil {
				return nil, errAmbiguous(id)
			}
			match = r
		}
	}
	if match == nil {
		return nil, errNoRun(id)
	}
	return match, nil
}

type runErr string

func (e runErr) Error() string { return string(e) }

func errAmbiguous(id string) error { return runErr("run id prefix \"" + id + "\" is ambiguous") }
func errNoRun(id string) error {
	return runErr("no run matching \"" + id + "\" (see `benchy results`)")
}
