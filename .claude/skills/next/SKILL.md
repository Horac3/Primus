---
name: next
description: Continue the project — pick up the next roadmap step from docs/progress.md, plan it, build it test-first, review it, and write the handoff for the next conversation.
argument-hint: "[step number]"
disable-model-invocation: true
---

You are continuing the project in a fresh conversation. Everything you need is in files.

## Live state
```!
echo "branch: $(git branch --show-current 2>/dev/null)"
git status --short 2>/dev/null | head -30
echo "--- recent commits"
git log --oneline -8 2>/dev/null
echo "--- docs/progress.md (current step + handoff)"
sed -n '/^## Current step/,/^## Log/p' docs/progress.md 2>/dev/null | grep -v '^## Log' | head -60
```

## 1. Load state
- Step: `$ARGUMENTS` if given; otherwise the first `[~]` step in the roadmap, else the first `[ ]`.
  Read the roadmap line for it in `docs/progress.md` if it is not shown above.
- Read only the requirement IDs the step names from `docs/requirements.md` (grep for them; do not read the whole file).
- Read `docs/decisions.md` or `docs/architecture.md` only if the step depends on them.

## 2. Check the repo
If the tree is dirty or the handoff says something is half-done, report it in two lines and ask how
to proceed before changing anything.

## 3. Plan (plan mode, Opus)
Enter plan mode. Use the `Explore` subagent for any code search wider than one or two greps.
Present, briefly:
- the step and its requirement IDs
- the behaviours you will test, one line each, in order
- files to create or change
- the commits you will make
- open questions (if any; ask them now, not mid-build)

Wait for approval.

## 4. Build (Sonnet)
Follow `/tdd` discipline: one behaviour per Red → Green → Refactor cycle, show each failing test before
writing code. Run checks through the `test-runner` subagent. If a failure is not obvious after one look,
hand it to the `debugger` subagent. Look up unfamiliar APIs with the `researcher` subagent.

## 5. Review
When the behaviours are done, run `/review` (the `reviewer` subagent on Opus). Fix blocking findings,
test-first where behaviour changes. Do not argue with findings in long form; fix or note why not in one line.

## 6. Commit
Run `/commit`.

## 7. Hand off
Run `/handoff` to mark the step `[x]` (or `[~]` if unfinished) and rewrite "Current step" and "Handoff notes".

## 8. Finish with exactly
`Step N complete. Run /clear, then /next to continue with step N+1: <title>.`

If context passes ~60% (status line yellow/red) before the step is done: finish the current cycle,
run `/handoff` with `[~]` and the precise next action, then tell the user to `/clear` and `/next`.
