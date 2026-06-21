package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/option-ai/benchy/cli/internal/snapshot"
	"os"
	"os/exec"
	"strings"
)

// repoURL normalizes a snapshot repo field into a clonable URL. Accepts full
// URLs, scp-style git@ remotes, and bare "github.com/owner/name" shorthand.
func repoURL(repo string) string {
	if strings.HasPrefix(repo, "http://") || strings.HasPrefix(repo, "https://") ||
		strings.HasPrefix(repo, "git@") || strings.HasPrefix(repo, "ssh://") {
		return repo
	}
	return "https://" + strings.TrimSuffix(repo, "/") + ".git"
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		// never hang on an ssh password prompt during clone fallbacks
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -oBatchMode=yes")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

// gitClone clones repo into dest, falling back through auth strategies so
// private repos work without interactive prompts: plain https → the gh CLI's
// credential helper → an ssh remote rewrite → the local path the eval was
// captured from (source_path). The aggregated error tells the user how to fix
// auth rather than just "exit status 128".
func gitClone(ctx context.Context, repo, sourcePath, dest string) error {
	url := repoURL(repo)
	var errs []string
	try := func(label string, args ...string) bool {
		_ = os.RemoveAll(dest) // a failed attempt may leave a partial dir
		if _, err := git(ctx, "", args...); err != nil {
			errs = append(errs, label+": "+strings.TrimSpace(err.Error()))
			return false
		}
		return true
	}

	if try("https", "clone", "--quiet", url, dest) {
		return nil
	}
	if strings.HasPrefix(url, "https://") {
		if _, err := exec.LookPath("gh"); err == nil {
			if try("gh-auth", "-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential",
				"clone", "--quiet", url, dest) {
				return nil
			}
		}
		// https://host/owner/name(.git) → git@host:owner/name.git
		rest := strings.TrimPrefix(url, "https://")
		if host, path, ok := strings.Cut(rest, "/"); ok {
			ssh := "git@" + host + ":" + strings.TrimSuffix(path, ".git") + ".git"
			if try("ssh", "clone", "--quiet", ssh, dest) {
				return nil
			}
		}
	}
	if sourcePath != "" {
		if fi, err := os.Stat(sourcePath); err == nil && fi.IsDir() {
			if try("local "+sourcePath, "clone", "--quiet", sourcePath, dest) {
				return nil
			}
		} else {
			errs = append(errs, "local: "+sourcePath+" not present on this machine")
		}
	}
	return fmt.Errorf("could not clone %s — if it is private, run `gh auth login`, set up ssh keys, or re-capture the eval on this machine so source_path applies. Attempts:\n  %s",
		repo, strings.Join(errs, "\n  "))
}

func gitFetch(ctx context.Context, dir string) error {
	_, err := git(ctx, dir, "fetch", "--all", "--quiet", "--tags")
	return err
}

// gitWorktreeAdd adds a detached worktree at commit, fetching first if the
// commit isn't present in the cache yet.
func gitWorktreeAdd(ctx context.Context, repoDir, wt, commit string) error {
	if _, err := git(ctx, repoDir, "cat-file", "-e", commit+"^{commit}"); err != nil {
		_ = gitFetch(ctx, repoDir)
	}
	_ = os.RemoveAll(wt) // a stale worktree from a prior run would block add
	_, _ = git(ctx, repoDir, "worktree", "prune")
	_, err := git(ctx, repoDir, "worktree", "add", "--detach", "--force", wt, commit)
	return err
}

// gitCaptureDiff stages everything (so new files show) and returns the diff
// against the checked-out commit.
func gitCaptureDiff(ctx context.Context, wt string) (string, error) {
	if _, err := git(ctx, wt, "add", "-A"); err != nil {
		return "", err
	}
	out, err := git(ctx, wt, "diff", "--cached")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// scratchWorkspace creates a fresh, empty git repo for a scratch (repo-less)
// eval, with an empty initial commit so a later `diff --cached` shows every
// file the agent creates. Identity is set inline so the commit needs no global
// git config.
func scratchWorkspace(ctx context.Context, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := git(ctx, dir, "init", "--quiet"); err != nil {
		return err
	}
	_, err := git(ctx, dir,
		"-c", "user.email=bench@local", "-c", "user.name=bench",
		"commit", "--allow-empty", "--quiet", "-m", "bench scratch baseline")
	return err
}

// cleanupWorkspace removes a job's working tree once its artifacts are saved.
// Repo-backed trees are removed via git so the cache repo's worktree registry
// stays clean; scratch dirs are plain removals. Best-effort with a fresh
// context: the job's ctx may already be cancelled.
func cleanupWorkspace(cacheRepo, wt string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if cacheRepo != "" {
		if _, err := git(ctx, cacheRepo, "worktree", "remove", "--force", wt); err == nil {
			return
		}
	}
	_ = os.RemoveAll(wt)
}

// resetWorkspace returns a job's tree to its pristine starting state between
// rate-limit retries, discarding any partial work from the failed attempt.
func resetWorkspace(ctx context.Context, e *snapshot.Snapshot, wt string) error {
	if e.IsScratch() {
		return scratchWorkspace(ctx, wt)
	}
	if _, err := git(ctx, wt, "reset", "--hard"); err != nil {
		return err
	}
	_, err := git(ctx, wt, "clean", "-fd")
	return err
}
