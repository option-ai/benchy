package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/snapshot"
	"github.com/option-ai/benchy/cli/internal/transcript"
	"github.com/spf13/cobra"
)

// captureCmd is the hook entrypoint. It is invoked by Claude Code's SessionStart
// ("start"), Stop ("end") and SessionEnd ("end --force") hooks with the hook
// payload on stdin. It must never disrupt the user's session: it always exits 0,
// logs failures to a file, and offloads the (potentially ~1s) transcript parse
// to a detached worker so the Stop hook returns instantly.
var (
	captureForce  bool
	captureWorker bool
	captureInput  string
)

var captureCmd = &cobra.Command{
	Use:    "capture [start|end]",
	Short:  "Hook entrypoint: prospectively capture a finished session as a candidate eval",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if captureWorker { // detached child doing the real work
			if err := runWorker(captureInput); err != nil {
				logCapture("worker error: %v", err)
			}
			return
		}
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			logCapture("read stdin (%s): %v", args[0], err)
			return
		}
		if err := dispatch(args[0], raw); err != nil {
			logCapture("error (%s): %v", args[0], err)
		}
		// Always succeed: a non-zero Stop hook would surface to the user.
	},
}

func init() {
	f := captureCmd.Flags()
	f.BoolVar(&captureForce, "force", false, "capture synchronously (used by the SessionEnd hook to guarantee a final capture)")
	f.BoolVar(&captureWorker, "worker", false, "internal: run as the detached capture worker")
	f.StringVar(&captureInput, "input", "", "internal: path to the hook payload for --worker")
	_ = f.MarkHidden("worker")
	_ = f.MarkHidden("input")
}

// hookInput is the subset of the Claude Code hook payload we read from stdin.
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
}

// pending is the sidecar written at SessionStart so the Stop capture can anchor
// to the commit that was HEAD *before* the work — recovering it after the fact
// is unreliable, which is the whole reason prospective capture beats mining.
type pending struct {
	SessionID   string `json:"session_id"`
	Cwd         string `json:"cwd"`
	Branch      string `json:"branch"`
	StartCommit string `json:"start_commit"`
	RepoSlug    string `json:"repo_slug"`
	SourcePath  string `json:"source_path"`
	StartTime   string `json:"start_time"`
}

func dispatch(phase string, raw []byte) error {
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("parse hook input: %w", err)
	}
	if in.SessionID == "" {
		return fmt.Errorf("hook input missing session_id")
	}
	switch phase {
	case "start":
		return captureStart(in) // cheap (git rev-parse); run inline
	case "end":
		if captureForce {
			return captureEndLocked(in, true) // SessionEnd: synchronous, guaranteed
		}
		return spawnWorker(in, raw) // Stop: offload so the hook returns instantly
	default:
		return fmt.Errorf("unknown capture phase %q", phase)
	}
}

// spawnWorker writes the hook payload to a temp file and launches a detached
// `capture end --worker` process, returning immediately. Parsing a multi-MB
// transcript can take ~1s; doing it inline would add that to every Stop. On any
// failure to launch, it falls back to capturing inline rather than dropping it.
func spawnWorker(in hookInput, raw []byte) error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(config.PendingDir(), "hookin-*.json")
	if err != nil {
		return captureEndLocked(in, false)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return captureEndLocked(in, false)
	}
	tmp.Close()

	self, err := os.Executable()
	if err != nil {
		os.Remove(tmp.Name())
		return captureEndLocked(in, false)
	}
	c := exec.Command(self, "capture", "end", "--worker", "--input", tmp.Name())
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survive the hook's process group
	if dn, e := os.OpenFile(os.DevNull, os.O_RDWR, 0); e == nil {
		c.Stdin, c.Stdout, c.Stderr = dn, dn, dn
	}
	if err := c.Start(); err != nil {
		os.Remove(tmp.Name())
		return captureEndLocked(in, false)
	}
	return c.Process.Release()
}

// runWorker is the detached child: it reads the payload file, captures, and
// cleans up the temp file.
func runWorker(inputPath string) error {
	if inputPath == "" {
		return fmt.Errorf("worker missing --input")
	}
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}
	defer os.Remove(inputPath)
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	if in.SessionID == "" {
		return fmt.Errorf("worker input missing session_id")
	}
	return captureEndLocked(in, false)
}

// captureEndLocked serializes captures for one session with a file lock so
// overlapping Stop turns don't race on the candidate file. A non-forced caller
// that can't get the lock (another capture is already in flight) coalesces and
// exits; the forced SessionEnd caller waits for the lock to guarantee a final,
// up-to-date capture.
func captureEndLocked(in hookInput, force bool) error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(config.PendingDir(), short(in.SessionID)+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return captureEnd(in) // best effort without a lock
	}
	defer lock.Close()
	how := syscall.LOCK_EX
	if !force {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(lock.Fd()), how); err != nil {
		return nil // another capture for this session is in flight; coalesce
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return captureEnd(in)
}

// captureStart records the session's base commit for later anchoring.
func captureStart(in hookInput) error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}
	p := pending{
		SessionID:   in.SessionID,
		Cwd:         in.Cwd,
		Branch:      gitOut(in.Cwd, "rev-parse", "--abbrev-ref", "HEAD"),
		StartCommit: gitOut(in.Cwd, "rev-parse", "HEAD"),
		RepoSlug:    repoSlug(in.Cwd),
		SourcePath:  gitOut(in.Cwd, "rev-parse", "--show-toplevel"),
		StartTime:   time.Now().UTC().Format(time.RFC3339),
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(pendingPath(in.SessionID), b, 0o644)
}

// captureEnd parses the finished session, filters it, and writes an accepted
// candidate (or removes a stale one). Idempotent: re-runs overwrite the same file.
func captureEnd(in hookInput) error {
	if in.TranscriptPath == "" {
		return fmt.Errorf("hook input missing transcript_path")
	}
	sess, err := transcript.ParseFile(in.TranscriptPath)
	if err != nil {
		return fmt.Errorf("parse transcript: %w", err)
	}
	sess.ID = in.SessionID
	verdict := transcript.Evaluate(sess)

	candDest := candidatePath(in.SessionID)
	snapDest := autoSnapshotPath(in.SessionID)
	if !verdict.Accept {
		// a previously-accepted/promoted session may have gone non-portable; drop
		// it from wherever it landed (queue or run set), assets included.
		removeEval(candDest)
		removeEval(snapDest)
		logCapture("skip %s: %s", short(in.SessionID), strings.Join(verdict.Reasons, "; "))
		return nil
	}

	cfg, _ := config.Load()
	dest, promoted := captureDest(cfg, verdict, candDest, snapDest)

	p := loadPending(in.SessionID)
	snap := buildSnapshot(sess, verdict, p, dest)
	if promoted {
		snap.Notes = "" // a promoted eval is a real run-set entry, not a review card
	}
	if err := config.EnsureDirs(); err != nil {
		return err
	}
	if err := saveImages(snap, sess.Images); err != nil {
		return err
	}
	if err := snap.Save(); err != nil {
		return err
	}
	// keep exactly one copy: clear the other location so a threshold change (or a
	// session that crossed the bar mid-run) never leaves a duplicate behind.
	if promoted {
		removeEval(candDest)
		logCapture("auto-promoted %s -> snapshots/%s (%s, score %d >= %d)", short(in.SessionID), filepath.Base(dest), verdict.Verdict, verdict.Score, cfg.AutoPromoteScore)
	} else {
		removeEval(snapDest)
		logCapture("captured %s -> candidates/%s (%s, score %d)", short(in.SessionID), filepath.Base(dest), verdict.Verdict, verdict.Score)
	}
	return nil
}

// captureDest decides where an accepted capture lands: the run set (snapshots/)
// when auto-promote is enabled and the score clears the bar, else the review
// queue (candidates/).
func captureDest(cfg config.Config, v transcript.Result, candDest, snapDest string) (dest string, promoted bool) {
	if cfg.AutoPromoteScore > 0 && v.Score >= cfg.AutoPromoteScore {
		return snapDest, true
	}
	return candDest, false
}

// removeEval deletes an eval markdown and its sibling assets dir, if present.
func removeEval(mdPath string) {
	os.Remove(mdPath)
	os.RemoveAll(strings.TrimSuffix(mdPath, ".md") + ".assets")
}

// buildSnapshot assembles the candidate eval. Repo anchoring requires the
// session-start sidecar; without it we downgrade to a scratch/answer eval rather
// than guess a wrong base commit.
func buildSnapshot(sess *transcript.Session, v transcript.Result, p *pending, dest string) *snapshot.Snapshot {
	s := &snapshot.Snapshot{Prompts: sess.Prompts, Path: dest}
	s.Title = deriveTitle(sess)
	s.Created = captureDate(sess.LastTS)
	s.Source = "auto"
	s.Replay = config.ReplayOneShot
	s.Notes = renderNotes(v, sess, p)

	if p != nil && p.StartCommit != "" && p.RepoSlug != "" {
		s.Repo = p.RepoSlug
		s.Commit = p.StartCommit
		s.SourcePath = p.SourcePath
		s.Gates = transcript.DetectGates(firstNonEmpty(p.SourcePath, p.Cwd))
	}
	// No diff produced (and not anchored as a code task) => grade the answer.
	if len(sess.EditedFiles) == 0 && sess.GitMutations == 0 {
		s.Expects = "answer"
	}
	return s
}

// saveImages writes captured reference images into the eval's sibling assets
// dir (replacing any prior set so re-capture stays idempotent) and records their
// filenames on the snapshot. A failure to write one image must not lose the eval.
func saveImages(s *snapshot.Snapshot, imgs []transcript.Image) error {
	s.Images = nil
	if len(imgs) == 0 {
		return nil
	}
	dir := s.AssetsDir()
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, img := range imgs {
		name := fmt.Sprintf("img-%d%s", i+1, img.Ext)
		if err := os.WriteFile(filepath.Join(dir, name), img.Data, 0o644); err != nil {
			return err
		}
		s.Images = append(s.Images, name)
	}
	return nil
}

func renderNotes(v transcript.Result, sess *transcript.Session, p *pending) string {
	var b strings.Builder
	b.WriteString("<!-- benchy auto-capture\n")
	fmt.Fprintf(&b, "verdict: %s (score %d)\n", v.Verdict, v.Score)
	if p == nil || p.StartCommit == "" || p.RepoSlug == "" {
		b.WriteString("anchor: none (session-start commit not recorded) — review before running\n")
	}
	if len(v.Reasons) > 0 {
		b.WriteString("warnings:\n")
		for _, r := range v.Reasons {
			fmt.Fprintf(&b, "  - %s\n", r)
		}
	}
	fmt.Fprintf(&b, "prompts: %d, files edited: %d, git ops: %d\n", len(sess.Prompts), len(sess.EditedFiles), sess.GitMutations)
	b.WriteString("Promote with: mv this file into ../snapshots/ after review.\n")
	b.WriteString("-->")
	return b.String()
}

// deriveTitle prefers the session's own title, else a slug of the first
// substantive prompt, else the session id.
func deriveTitle(sess *transcript.Session) string {
	if t := strings.TrimSpace(sess.Title); t != "" {
		return t
	}
	for _, p := range sess.Prompts {
		words := strings.Fields(p)
		if len(words) >= 3 {
			if len(words) > 7 {
				words = words[:7]
			}
			return strings.Join(words, " ")
		}
	}
	return "auto " + short(sess.ID)
}

func captureDate(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("2006-01-02")
	}
	return time.Now().UTC().Format("2006-01-02")
}

// --- small helpers ---

func pendingPath(id string) string { return filepath.Join(config.PendingDir(), id+".json") }
func candidatePath(id string) string {
	return filepath.Join(config.CandidatesDir(), "auto-"+short(id)+".md")
}

// autoSnapshotPath is where an auto-promoted capture lands in the run set. It is
// keyed by session id (not title slug) so per-turn re-captures overwrite in place
// and a later downgrade can find and remove it.
func autoSnapshotPath(id string) string {
	return filepath.Join(config.SnapshotsDir(), "auto-"+short(id)+".md")
}

func loadPending(id string) *pending {
	b, err := os.ReadFile(pendingPath(id))
	if err != nil {
		return nil
	}
	var p pending
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}

func gitOut(dir string, args ...string) string {
	if dir == "" {
		return ""
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoSlug normalizes the origin remote to github.com/owner/name.
func repoSlug(dir string) string {
	url := gitOut(dir, "remote", "get-url", "origin")
	if url == "" {
		return ""
	}
	url = strings.TrimSuffix(strings.TrimSpace(url), ".git")
	if i := strings.Index(url, "github.com"); i >= 0 {
		rest := url[i+len("github.com"):]
		rest = strings.TrimLeft(rest, ":/")
		return "github.com/" + rest
	}
	return ""
}

func logCapture(format string, args ...any) {
	if err := config.EnsureDirs(); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(config.Dir(), "capture.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s "+format+"\n", append([]any{time.Now().UTC().Format(time.RFC3339)}, args...)...)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
