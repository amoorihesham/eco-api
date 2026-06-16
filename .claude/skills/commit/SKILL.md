---
name: commit
description: Commit the current working-tree changes to git with a clear, conventional eco-api commit message (a PREFIX line plus an optional second detail line — two lines at most). Use when asked to "commit", "commit my changes", or "save this to git".
---

# Committing eco-api changes

Stage the current changes and create **one** commit whose message highlights what changed in
**two lines at most**, following this repo's existing convention.

## Workflow

**1. Inspect what's changing.** Don't commit blind — see the actual diff so the message reflects it:

```bash
git status
git diff HEAD
```

If nothing is staged or modified, stop and tell the user there's nothing to commit.

**2. Stage the relevant changes.** Default to staging everything that's part of this logical change:

```bash
git add -A
```

If the working tree mixes unrelated changes, stage only the files for the change being committed
and mention what you left out.

**3. Write the message** following the house style below, then commit. Use a heredoc so the body
is literal:

```bash
git commit -F - <<'EOF'
PREFIX: concise summary of the change
optional second line with the one important detail
EOF
```

**4. Confirm.** Run `git log -1 --stat` and report the commit to the user.

## Message convention (match the existing log)

Recent history uses an **uppercase `PREFIX: lowercase summary`** first line:

```
MODULE: p-3 identity & auth implemented
FIX: postgresql version in compose-file & migration applied
PACKAGE: env package implemented
PLANNING: write the execution plan for the p3 - IDENTITY_MODULE
FOUNDATIONS: p-2 eventing foundation
```

Rules:

- **Two lines maximum.** Line 1 is the summary; line 2 (optional) adds the single most useful detail.
  Drop line 2 if the summary already says it all — a one-line commit is fine.
- **Line 1 ≤ ~70 chars**, present-tense, starts with a `PREFIX:`. Pick the prefix from the change's
  nature — common ones here: `MODULE`, `PACKAGE`, `FIX`, `FEATURE`, `FOUNDATIONS`, `PLANNING`,
  `DOCS`, `REFACTOR`, `TEST`, `CHORE`. Reuse an existing prefix when one fits; run
  `git log --oneline -15` if unsure what's in use.
- Reference the **phase tag** (P3, P4, …) when the change belongs to a phase.
- Describe **what changed and why**, not a file list — the diff already lists files.

## Rules

- **Create a new commit; never amend** unless the user explicitly asks.
- **Don't push.** This skill only commits locally. Push only if the user asks.
- **Only commit when asked.** If currently on `main` and the change is substantial, mention that a
  feature branch might be cleaner — but still commit to `main` if that's what the user wants.
- End the commit message with the trailer (separated by a blank line) **only if the user's harness
  requires it** — otherwise keep it to the two content lines.
