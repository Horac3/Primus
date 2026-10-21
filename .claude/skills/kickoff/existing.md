# /kickoff for an existing project

The repo already has code, and maybe docs, rules for other AI tools, and work in flight. The goal is to
**capture what is already known, not rediscover it**, then leave a lean CLAUDE.md, a codebase map, and a
roadmap that starts from where the project actually is. Never delete or move the user's files.

## A. Read what already exists (cheapest signal first)

1. **History.** Run `.claude/kit/context.sh history 30`. Note: what has been worked on lately, the hotspots
   (most-changed files: where bugs and churn live), active branches, TODO-dense files, open issues.
2. **Instruction files** listed in the snapshot (CLAUDE.md, AGENTS.md, .cursorrules, .cursor/rules,
   copilot-instructions, GEMINI.md, …): read each in full if under ~300 lines, otherwise headings first and
   then the relevant sections. They hold rules someone has already learned the hard way.
3. **Project docs:** README and CONTRIBUTING in full (setup, conventions, how to run). For `docs/**`,
   read headings only (`grep -n '^#'`) and open a doc only when the map needs it.
4. **Decision records:** titles are in the snapshot. Read an ADR only if it constrains how code is written.
5. **Contracts:** note API specs, schemas, and migration dirs. Do not read them in full.

Keep a running list of **rules** (things Claude must or must not do) and **facts** (how the system works),
each with its source file.

## B. Map the code

Launch up to 3 `Explore` subagents **in parallel**. Brief each one with the layout and hotspots from the
snapshot and the facts the docs already state, so they **verify and fill gaps** instead of starting cold:
- entry points, wiring, and how a request or job flows end to end
- the domain and data model: where business rules live, persistence, key types
- tests, build, CI and deploy: how tests are organised, what is slow or flaky, how it ships

Ask each for at most ~150 words with file paths, plus anything that **contradicts** the docs.

Write `docs/architecture.md` (≤ ~120 lines). Its first line must be the marker that `/remap` and the
session hook rely on, with the real short hash from `git rev-parse --short HEAD`:

```
<!-- mapped-at: <short-sha> <YYYY-MM-DD> -->
# Architecture
## Purpose            (2–3 lines)
## Layout             (2-level tree with one-line roles)
## How a request flows
## Domain and data
## Tests and tooling  (how to run, what is slow or flaky)
## Build and deploy
## Hotspots           (from history: the files with the most churn, and why if known)
## Known debt and contradictions   (docs vs code, TODO clusters)
```

## C. Interview: only the gaps and conflicts (one round)

Ask only what code, docs, and history cannot answer: product intent and users if the README does not say,
which contradictions are intended, rules that are non-negotiable, commit scopes, and **what the user wants
to work on next**. Skip anything already answered.

## D. CLAUDE.md

**Case 1: `CLAUDE.md` is the kit template** (contains `TODO(kickoff)`). Fill it from the rules and facts
collected above (under ~120 lines), as in step 2 of SKILL.md.

**Case 2: the project had its own CLAUDE.md** (`CLAUDE.md.kit-new` exists, or there is no `TODO(kickoff)`).
Restructure it instead of replacing it:

1. Back it up first: `cp CLAUDE.md docs/CLAUDE.md.pre-kit` (the user approves the command).
2. Classify every rule and section. Each one goes to exactly one place:
   - **Drop as duplicate**: only if `.claude/rules/kit/*` already says the same thing. Name the kit file.
   - **Path-scoped rule**: applies only to part of the code. Move it to `.claude/rules/<topic>.md` with
     `paths:` globs that match **real** directories (check with `git ls-files | grep`).
   - **Reference doc**: long background needed only sometimes. Move it to `docs/<topic>.md` and add a
     one-line pointer in CLAUDE.md.
   - **Keep**: product, stack, architecture rules, commit scopes, project-specific working rules.
3. Show the user a table **original section → destination**, plus line counts before and after.
   **No rule may disappear without a row in the table.** Wait for approval.
4. Apply it with Edit (CLAUDE.md) and Write (new rule and doc files). Use `CLAUDE.md.kit-new` only as a
   structural reference; never copy its placeholders in.

**Rules from other AI tools** (AGENTS.md, .cursorrules, …): fold the useful ones into the same table.
Leave those files in place; tell the user they can remove them later. Do not edit AGENTS.md (a hook blocks it).

## E. Decisions

First detect the entry style `docs/decisions.md` already uses (`grep -n '^## ' docs/decisions.md`). If it is
not the kit's `## D-NNN: <title>`, e.g. `## <Topic> (step N)`, keep that style and record it in a project rule
so `/decide` follows it: `.claude/rules/decisions.md` with `paths: ["docs/decisions.md"]` and one line, such as
`Entries use "## <Topic> (step N)", not D-NNN.`

If ADRs exist, append index entries to `docs/decisions.md` in that style, linking instead of copying
(kit style: `## D-0NN: <title>` · `Status: accepted (imported)` · `See: <path>`). Add one entry for each
contradiction the user confirmed as intended.

## F. Requirements and roadmap (continue at step 4 of SKILL.md for commands first)

An existing project rarely has `FR-` requirements. Do not reverse-engineer the whole system into them.
- Build a **candidate list** from: what the user said they want next, active branches, recent commit themes,
  TODO clusters, and open issues. Show it, and let the user pick and order.
- Write requirements **only for the picked work**, in the `docs/requirements.md` format.
- Before a step that changes a hotspot with thin tests, propose a **characterisation-test step**: pin current
  behaviour first, so refactors are safe. It is TDD's safety net for legacy code.
- If `docs/progress.md` existed before the kit, keep its content and add any missing headings
  (`## Roadmap`, `## Current step`, `## Handoff notes`, `## Log`) with Edit.
- **Oversized handoffs.** Measure the section:
  `awk '/^## Handoff notes/{f=1;next} /^## /{f=0} f' docs/progress.md | wc -l`. Over ~60 lines, every session
  pays for it. Propose keeping the newest handoff and moving the older ones **verbatim** to
  `docs/progress-archive.md`. Move them **by script, never by retyping**. Show the line range (`grep -n` the
  boundaries), and after approval run, with `A`-`B` the inclusive line range to move:
  ```bash
  f=docs/progress.md a=docs/progress-archive.md A=<first> B=<last>
  [ -f "$a" ] || printf '# Progress archive\n\nOlder handoff notes, moved verbatim from docs/progress.md.\n\n' >"$a"
  before=$(wc -l <"$f"); sed -n "${A},${B}p" "$f" >>"$a"
  rest="$(awk -v a="$A" -v b="$B" 'NR<a || NR>b' "$f")"; printf '%s\n' "$rest" >"$f"
  echo "moved $((before - $(wc -l <"$f"))) of $((B - A + 1)) lines"
  ```
  Add a one-line pointer to the archive under "Handoff notes", and add `## Log` if it is missing.

Then return to SKILL.md step 5 (approval) and step 6 (finish).
