---
name: handoff
description: Write the session handoff into docs/progress.md so the next conversation can resume with /next. Use at the end of a step, or mid-step when context is getting large.
argument-hint: "[done | partial]"
effort: low
---

Update `docs/progress.md` with Edit (never rewrite the file). Keep it factual and short; the next
session reads this cold.

1. **Roadmap:** mark the step `[x]` if all its behaviours are done, reviewed and committed;
   otherwise `[~]`. Do not touch other steps.
2. **"## Current step"** — replace the section body with:
   ```
   Step N: <title>
   Requirements: <IDs>
   Status: <done | in progress — k of n behaviours done>
   ```
   If the step is done, describe the *next* step here instead (title and IDs from the roadmap).
3. **"## Handoff notes"** — replace the section body with at most ~15 lines:
   ```
   - Done: <behaviours finished this session, with test names>
   - Next action: <the exact next thing to do: file, function, test to write>
   - Uncommitted: <files, or "none">
   - Decisions: <anything decided this session that is not yet in docs/decisions.md>
   - Watch out: <traps, flaky tests, things that looked wrong but were not touched>
   ```
4. **"## Log"** — append one line: `- YYYY-MM-DD step N <done|partial> (<short commit hash or "uncommitted">)`.
5. If a decision was made, append it to `docs/decisions.md` too.
6. If this step added, removed or renamed packages, modules or top-level directories, add
   `- Run /remap: <what moved>` to the handoff notes.

Commit only if the user has asked for commits in this session: `docs: update progress after step N`.

Then tell the user exactly one line:
- done → `Step N complete. Run /clear, then /next to continue with step N+1: <title>.`
- partial → `Handoff written. Run /clear, then /next to resume step N at: <next action>.`
