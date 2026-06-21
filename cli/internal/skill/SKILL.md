---
name: add-to-benchy
description: Capture the current conversation as a benchy eval — grabs all user prompts (and, if you're in a git repo, the repo + commit) and writes a snapshot the `benchy` CLI can replay against other models. Works with or without a repo, so it's usable from coding sessions and from repo-less environments alike. Use when the user says "add this to my benchy", "/add-to-benchy", "snapshot this for evals", or wants to turn the current session into a benchmark case.
argument-hint: "[feedback…]"
arguments: [feedback]
allowed-tools: Read Write Bash(git *) Bash(mkdir *)
---

# add-to-benchy

Capture the current session as a **benchy eval**: a markdown snapshot of the user
prompts (plus repo state when available) that the `benchy` CLI replays against
coding models and scores with a blind judge.

An eval comes in two flavours, chosen automatically:

- **repo-backed** — you're inside a git repo. Capture the repo + commit so benchy
  replays the prompts against that exact code state and judges the diff.
- **scratch** — no git repo (e.g. a from-scratch task, or a session in Claude
  Desktop / ChatGPT desktop / Cowork). Capture prompts only; benchy runs the
  agent in a fresh empty workspace and judges whatever it produces (created
  files and/or its written answer).

## Arguments (optional)

The raw invocation arguments are: `$ARGUMENTS`

- **feedback** — the ENTIRE argument string, as one string (it is normally a
  sentence). If there are no arguments, leave the feedback frontmatter line
  out. The judge scores against this note, so keep it verbatim.

You decide the **title**: infer a short kebab-case slug (2–5 words) from what
the session was actually about — name the task, not the repo (e.g.
`fix-flaky-auth-test`, not `api-changes`). If the user explicitly states a
title in prose (e.g. "call it X"), honor that instead.

## Steps

1. **Collect the user prompts.** Gather every *user* message in the current
   conversation, in order — only genuine user turns, not tool results, system
   reminders, or your own messages. Preserve their full text verbatim, with two
   exclusions:
   - the `/add-to-benchy` invocation itself (it is bookkeeping, not part of the
     task being benchmarked), and
   - any other slash-command invocations (`/foo ...`) that are harness
     commands rather than task content.
   If a prompt happens to contain the literal line `<!-- prompt -->`, indent it
   by two spaces inside the captured block so benchy's splitter is not confused.

2. **Determine the anchor.** Check whether the working directory is a git repo
   (`git rev-parse --is-inside-work-tree`). 
   - **If yes (repo-backed):**
     - `git remote get-url origin` → normalize to `github.com/owner/name`
       (strip protocol, trailing `.git`).
     - `git rev-parse HEAD` → the commit.
     - `git rev-parse --show-toplevel` → `source_path` (the absolute repo root
       on this machine — benchy's clone fallback when the remote is private).
     - If the tree is dirty, warn that uncommitted changes won't be captured
       (benchy checks out the commit cleanly).
     - Detect gate commands and fill what you find (leave unknown ones empty):
       Node/Bun `package.json` scripts; Go `go build ./...` / `go test ./...` /
       `go vet ./...`; Rust `cargo build|test|clippy`; Python `pytest` / `ruff`.
   - **If no (scratch):** skip repo, commit, and gates entirely. Do not invent
     them. The eval will run in an empty workspace.

3. **Write the snapshot file** to `~/.config/benchy/snapshots/<title-slug>.md`
   (create the directory if needed), in EXACTLY this format so `benchy` can parse
   it. **Omit `repo`, `commit`, and the `gates` block for scratch evals.**

   Repo-backed:
   ```markdown
   ---
   title: <title>
   repo: github.com/owner/name
   commit: <full-sha>
   source_path: /absolute/path/to/repo
   created: <YYYY-MM-DD>
   feedback: <feedback or omit the line if none>
   replay: oneshot
   gates:
       build: <cmd or omit>
       test: <cmd or omit>
       lint: <cmd or omit>
   ---

   ## Prompts

   <!-- prompt -->
   <first user prompt verbatim>

   <!-- prompt -->
   <second user prompt verbatim>
   ```

   Scratch (no repo):
   ```markdown
   ---
   title: <title>
   created: <YYYY-MM-DD>
   feedback: <feedback or omit the line if none>
   replay: oneshot
   ---

   ## Prompts

   <!-- prompt -->
   <first user prompt verbatim>
   ```

   - Each prompt block is preceded by a literal `<!-- prompt -->` line — this is
     how benchy splits prompts, so include it before every prompt.
   - `replay: oneshot` is the default (all prompts collapsed into one). Use
     `replay: sequential` only if the user asks to preserve turn-by-turn replay.
   - Optionally add `expects: diff | answer | conversation` when the deliverable
     is clear: `answer` if the task is a question (file edits would be off-task),
     `conversation` if the feedback is about behavior across turns (e.g. "should
     have told me I was wrong early" — this forces sequential replay), `diff`
     for pure code changes. Omit it otherwise; the judge infers.

4. **Confirm** to the user: print the path written, the title, the anchor
   (repo@commit, or "scratch"), the number of prompts, and any detected gates.
   Tell them they can run it with `benchy run`.

## Notes

- Never invent or paraphrase prompts — capture them exactly.
- The eval is self-contained: prompts + optional repo/commit + feedback rubric.
  There is no reference solution; the judge scores against the feedback note and
  (when present) the deterministic gates.
- If you cannot run git at all (no shell access), treat it as a scratch eval.
