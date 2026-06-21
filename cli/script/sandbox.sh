#!/usr/bin/env bash
#
# sandbox.sh — exercise the full install → capture → promote flow in a throwaway
# environment, touching nothing on your real machine.
#
# It overrides HOME (where the skill + Claude Code hooks install) and BENCHY_HOME
# (where ~/.config/benchy lives) to temp dirs, builds the binary, then simulates
# the SessionStart/Stop hooks with synthetic transcripts — including one with an
# embedded image and one that must be rejected for MCP dependence.
#
# It stops before `benchy run`, which shells out to real agent CLIs (logins +
# network + cost) and so can't be sandboxed for free.
#
# Usage:
#   script/sandbox.sh            # run, assert, clean up
#   script/sandbox.sh --keep     # leave the sandbox dir for inspection
set -euo pipefail

KEEP=0
[ "${1:-}" = "--keep" ] && KEEP=1

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SANDBOX="$(mktemp -d)"
BIN="$SANDBOX/bin/benchy"
mkdir -p "$SANDBOX/bin"

# Build FIRST, under the real environment, so Go uses the real module cache
# instead of writing (read-only) module files into the sandbox HOME.
printf '\033[1m%s\033[0m\n' "0. build benchy"
( cd "$REPO_ROOT" && go build -o "$BIN" . )

# Now isolate: HOME controls where the skill + Claude Code hooks install;
# BENCHY_HOME controls ~/.config/benchy. Both point into the throwaway sandbox.
export HOME="$SANDBOX/home"
export BENCHY_HOME="$SANDBOX/benchy"
mkdir -p "$HOME" "$BENCHY_HOME"

# Pin auto-promote OFF so this harness deterministically exercises the
# candidates -> promote review path, independent of the shipped default
# (which auto-promotes near-pristine captures straight into the run set).
printf '{"version":1,"auto_promote_score":0}\n' > "$BENCHY_HOME/config.json"

pass() { printf '  \033[32m✓\033[0m %s\n' "$1"; }
fail() { printf '  \033[31m✗ %s\033[0m\n' "$1"; exit 1; }
step() { printf '\n\033[1m%s\033[0m\n' "$1"; }

cleanup() {
  if [ "$KEEP" = "1" ]; then
    echo; echo "sandbox kept at: $SANDBOX"
  else
    rm -rf "$SANDBOX"
  fi
}
trap cleanup EXIT

echo "sandbox: $SANDBOX"
echo "HOME=$HOME"
echo "BENCHY_HOME=$BENCHY_HOME"
pass "built $BIN"

step "1. benchy install (skill + capture hooks + config dirs)"
"$BIN" install >/dev/null
SETTINGS="$HOME/.claude/settings.json"
[ -f "$HOME/.claude/skills/add-to-benchy/SKILL.md" ] || fail "skill not installed"
pass "skill installed"
grep -q "capture start" "$SETTINGS" || fail "SessionStart hook not wired"
grep -q "capture end" "$SETTINGS" || fail "Stop hook not wired"
pass "capture hooks wired into settings.json"
# idempotency: a second install must not duplicate the hooks
"$BIN" install >/dev/null
[ "$(grep -c 'capture start' "$SETTINGS")" = "1" ] || fail "install not idempotent (duplicate hooks)"
pass "install is idempotent"

step "2. create a throwaway project git repo"
PROJ="$SANDBOX/project"
mkdir -p "$PROJ"
git -C "$PROJ" init -q
git -C "$PROJ" config user.email sandbox@example.com
git -C "$PROJ" config user.name sandbox
git -C "$PROJ" remote add origin https://github.com/acme/widget.git
echo "package main" > "$PROJ/main.go"
printf 'module github.com/acme/widget\n\ngo 1.25\n' > "$PROJ/go.mod"
git -C "$PROJ" add -A
git -C "$PROJ" commit -q -m "initial"
BASE_COMMIT="$(git -C "$PROJ" rev-parse HEAD)"
pass "repo at $PROJ (HEAD ${BASE_COMMIT:0:8})"

step "3. SessionStart hook → record base commit"
SID="11111111-1111-1111-1111-111111111111"
printf '{"session_id":"%s","cwd":"%s","hook_event_name":"SessionStart"}' "$SID" "$PROJ" | "$BIN" capture start
SIDECAR="$BENCHY_HOME/pending/$SID.json"
[ -f "$SIDECAR" ] || fail "pending sidecar not written"
grep -q "$BASE_COMMIT" "$SIDECAR" || fail "sidecar missing base commit"
grep -q "github.com/acme/widget" "$SIDECAR" || fail "sidecar missing repo slug"
pass "sidecar anchored to base commit + repo"

step "4a. Stop hook → GOOD session (clean task + edit + embedded image)"
PNG_B64="$(printf '\x89PNG\r\n\x1a\nSANDBOX-IMAGE' | base64 | tr -d '\n')"
GOOD_TP="$SANDBOX/good.jsonl"
cat > "$GOOD_TP" <<EOF
{"type":"ai-title","title":"add a greeting flag to widget"}
{"type":"user","timestamp":"2026-06-19T10:00:00.000Z","gitBranch":"feat/greet","cwd":"$PROJ","message":{"content":[{"type":"text","text":"add a --greeting flag to the widget CLI that matches the mockup I pasted, with sensible defaults"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"$PNG_B64"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"$PROJ/main.go"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git add -A && git commit -m x"}}]}}
EOF
printf '{"session_id":"%s","transcript_path":"%s","cwd":"%s","hook_event_name":"Stop"}' "$SID" "$GOOD_TP" "$PROJ" | "$BIN" capture end --force
CAND="$(ls "$BENCHY_HOME"/candidates/auto-*.md 2>/dev/null | head -1)"
[ -n "$CAND" ] && [ -f "$CAND" ] || fail "good session was not captured"
pass "candidate written ($(basename "$CAND"))"
grep -q "repo: github.com/acme/widget" "$CAND" || fail "candidate not anchored to repo"
grep -q "commit: $BASE_COMMIT" "$CAND" || fail "candidate not anchored to base commit"
pass "candidate anchored repo@commit"
grep -q "images:" "$CAND" || fail "candidate missing images frontmatter"
IMG="${CAND%.md}.assets/img-1.png"
[ -f "$IMG" ] || fail "embedded image not saved to assets"
grep -q "SANDBOX-IMAGE" "$IMG" || fail "saved image bytes are wrong"
pass "embedded image extracted + saved ($(wc -c < "$IMG" | tr -d ' ') bytes)"
grep -q 'build: go build' "$CAND" || fail "go gates not detected"
pass "build/test/lint gates auto-detected"

step "4b. Stop hook → BAD session (MCP dependence) must be rejected"
BAD_TP="$SANDBOX/bad.jsonl"
BAD_SID="22222222-2222-2222-2222-222222222222"
cat > "$BAD_TP" <<EOF
{"type":"user","timestamp":"2026-06-19T11:00:00.000Z","gitBranch":"chore/x","cwd":"$PROJ","message":{"content":"analyze our posthog errors over the last day and propose fixes"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__posthog__exec","input":{}}]}}
EOF
printf '{"session_id":"%s","transcript_path":"%s","cwd":"%s","hook_event_name":"Stop"}' "$BAD_SID" "$BAD_TP" "$PROJ" | "$BIN" capture end --force
[ -f "$BENCHY_HOME/candidates/auto-22222222.md" ] && fail "MCP session was captured (should be rejected)"
[ -f "$CAND" ] || fail "rejecting the MCP session clobbered the good candidate"
grep -q "depends on MCP" "$BENCHY_HOME/capture.log" || fail "MCP rejection not logged"
pass "MCP session rejected (good candidate untouched)"

step "4c. async Stop hook (detached worker) writes a candidate"
ASID="33333333-3333-3333-3333-333333333333"
ASYNC_TP="$SANDBOX/async.jsonl"
cat > "$ASYNC_TP" <<EOF
{"type":"ai-title","title":"async capture works"}
{"type":"user","timestamp":"2026-06-19T12:00:00.000Z","gitBranch":"feat/async","cwd":"$PROJ","message":{"content":"add a structured logger with levels and a unit test covering each level"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"$PROJ/log.go"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git add -A && git commit -m x"}}]}}
EOF
# no --force: this returns immediately and a detached worker does the capture
printf '{"session_id":"%s","transcript_path":"%s","cwd":"%s","hook_event_name":"Stop"}' "$ASID" "$ASYNC_TP" "$PROJ" | "$BIN" capture end
ACAND="$BENCHY_HOME/candidates/auto-33333333.md"
for i in $(seq 1 50); do [ -f "$ACAND" ] && break; sleep 0.1; done
[ -f "$ACAND" ] || fail "detached worker did not write the candidate within 5s"
pass "async capture landed ($(basename "$ACAND"))"
rm -f "$ACAND"; rm -rf "${ACAND%.md}.assets"   # keep the promote step's queue clean

step "5. benchy candidates (review queue)"
"$BIN" candidates

step "6. promote the candidate into the run set"
"$BIN" candidates promote "$(basename "$CAND" .md)" >/dev/null
SNAP="$BENCHY_HOME/snapshots/add-a-greeting-flag-to-widget.md"
[ -f "$SNAP" ] || fail "promoted snapshot not found"
[ -f "$BENCHY_HOME/snapshots/add-a-greeting-flag-to-widget.assets/img-1.png" ] || fail "assets not moved on promote"
[ -f "$CAND" ] && fail "candidate not removed after promote"
pass "promoted to snapshots/ with assets moved"

step "7. benchy list (run set)"
"$BIN" list

step "capture.log"
cat "$BENCHY_HOME/capture.log"

echo
printf '\033[1;32mALL SANDBOX CHECKS PASSED\033[0m — nothing touched your real ~/.claude or ~/.config/benchy\n'
