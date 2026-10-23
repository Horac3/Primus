---
name: remap
description: Refresh docs/architecture.md from what changed in git since it was last mapped, without re-exploring the whole codebase.
argument-hint: "[area to focus on, or 'full']"
disable-model-invocation: true
context: fork
agent: general-purpose
model: sonnet
effort: medium
background: false
---

Refresh `docs/architecture.md` so it matches the code again. Focus requested (may be empty): $ARGUMENTS

## Drift (gathered by script)
```!
.claude/kit/context.sh drift 2>/dev/null
echo "## HEAD"; git rev-parse --short HEAD 2>/dev/null; date +%F
true
```

## Rules
- **No map yet, no valid marker, or `$ARGUMENTS` is `full`:** do a full map as described in
  `.claude/skills/kickoff/existing.md` section B (use `.claude/kit/context.sh layout` and `history`; parallel
  `Explore` subagents), then write the file with the marker.
- **Otherwise update incrementally.** Look only at the directories listed under structural changes, the changed
  dependency manifests, and the missing paths. Use `git diff <mapped-sha> HEAD -- <dir>` with `--stat` first, and
  read code only where the map's statements might now be wrong. Do not re-read unchanged areas.
- Change `docs/architecture.md` with **Edit**, section by section; never rewrite the whole file. Keep it under
  ~120 lines: when adding, compress or remove what no longer matters.
- Update the first-line marker to `<!-- mapped-at: <HEAD short sha> <today> -->`.
- If a missing path is also named in `CLAUDE.md` or in a `paths:` glob under `.claude/rules/`, **do not edit those**.
  Report them so the main conversation can fix them with the user.

## Return (≤ 12 lines)
```
REMAPPED: <old-sha> → <new-sha> (<n> commits)
Sections changed: <list>
Stale references in CLAUDE.md / rules: <list or "none">
Worth a decision record: <structural change that looks like an undocumented decision, or "none">
```
