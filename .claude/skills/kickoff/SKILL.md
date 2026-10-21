---
name: kickoff
description: One-time project setup after installing claude-kit — gather context (existing code, docs, other AI-tool rules, git history), fill or slim CLAUDE.md, verify the check commands, and draft the roadmap in docs/progress.md.
argument-hint: "[short product description]"
disable-model-invocation: true
model: opus
effort: high
---

Set this project up for the claude-kit workflow. Run this as the first message of a fresh session.
Product description from the user (may be empty): $ARGUMENTS

## Snapshot (gathered by script; do not re-gather it with tools)
```!
echo "mode: $(.claude/kit/context.sh mode 2>/dev/null)"
.claude/kit/context.sh layout 2>/dev/null
.claude/kit/context.sh existing 2>/dev/null
echo "## Check commands"; grep -E '^CHECK_' .claude/kit/commands.env 2>/dev/null
echo "## progress.md headings"; grep -E '^## ' docs/progress.md 2>/dev/null
true
```

**If mode is `existing`, follow [existing.md](existing.md) instead of steps 1–3 below**, then continue at step 4.
It covers reading existing docs and rules, git history, slimming a pre-existing CLAUDE.md, and
building the roadmap from work already in flight.

## 1. Interview (greenfield; one round)
Ask, in one AskUserQuestion batch where possible, only what `$ARGUMENTS` does not answer: product in two
sentences, users and launch market, constraints, non-negotiable architecture rules, commit scopes,
anything Claude must never do in this repo.

## 2. Fill CLAUDE.md
Replace every `TODO(kickoff)` in `CLAUDE.md` using Edit. Hard limit: under ~120 lines. Anything long or
needed only sometimes goes in `docs/` (read on demand) or in `.claude/rules/<topic>.md` with `paths:`
frontmatter (loads only when matching files are touched). Do not repeat `.claude/rules/kit/`.

## 3. Requirements
If `docs/requirements.md` is still the template, draft requirements with the user in its format
(`FR-AREA-NN`, given/when/then, edge cases). Keep the first version to what the first few steps need.

## 4. Verify the check commands
Run `.claude/kit/check.sh all` via `test-runner`. If a command is wrong for this repo (package manager,
monorepo layout, missing linter), fix `.claude/kit/commands.env` and re-run until each target is PASS,
FAIL for real reasons (say which), or deliberately empty (SKIP).

## 5. Roadmap
Group requirements into steps in `docs/progress.md` under `## Roadmap`, one conversation's work each
(1–5 behaviours), ordered by dependency, riskiest domain logic early:
`- [ ] N. <title> — <requirement IDs>`. Set "Current step" to step 1. Show it and wait for approval.
`docs/progress.md` must keep the headings `## Roadmap`, `## Current step`, `## Handoff notes`, `## Log`;
add any that are missing with Edit, keeping existing content.

## 6. Finish
List files created or changed (and any `*.kit-new` files the user should review), then say:
`Kickoff complete. Run /clear, then /next to start step 1: <title>.`
