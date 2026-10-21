# Progress

State lives here, not in chat history. `/next` reads "Current step" and "Handoff notes";
`/handoff` rewrites them. Markers: `[ ]` todo · `[~]` in progress · `[x]` done.

## Roadmap
- [x] 1. Credential pools: types, storage, login phase, UI, smoke test (`848a90c`..`16afb8c`)
- [x] 2. Fix: a run with zero completed requests never passes (`3bc3962`)
- [x] 3. Fix: cancelled in-flight requests counted separately from errors (`64b7c95`, `6e20275`)
- [ ] 4. Fix: flaky `TestProbe_SuccessfulTarget` (asserts `LatencyMs > 0` on localhost) — spec: `agent_instructions/fix-verdict-and-flaky-probe.md` (Fix 2)
- [ ] 5. Fix: per-field masking for credential pools — FR-CRED-01..04 — spec: `agent_instructions/fix-credential-field-masking.md`
- [ ] 6. Chore: `scripts/race.*` land in CI (race job in the workflow), and the Docker Hub publish job waits for it — NFR-CI-01
- [ ] 7. Open-loop fixed req/s mode — FR-RATE-01..04 (draft; run `/decide` on the dispatcher design first)
- [ ] 8. LLM: querying run history in natural language (spec not written)
- [ ] 9. Downloadable test reports and insights (not scoped)

## Current step
Step 4: Fix flaky `TestProbe_SuccessfulTarget`
Requirements: —
Status: not started

## Handoff notes
- Next action: run `/next`. Reproduce with `go test ./internal/engine -run TestProbe_SuccessfulTarget -count=50`
  (use `scripts/race.sh` or the Docker image if Go 1.25 is not installed), then change the assertion at
  `internal/engine/engine_phase4_test.go:152` from `LatencyMs <= 0` to `< 0`. A localhost probe can measure 0 ms.
  Do not add sleeps to the test server. Search other tests for the same `> 0` pattern on measured durations.
- Done in the session that installed claude-kit: kit installed, `CLAUDE.md` and path rules written, docs/progress,
  decisions, requirements and architecture filled in, `docs/docs.go` embed narrowed to the public pages,
  `CONTRIBUTING.md` updated, `scripts/race.*` and the `concurrency-reviewer` agent added.
- Go 1.25 could not be downloaded in that session, so `check.sh all` was NOT run there. First thing in the next
  session: run `.claude/kit/check.sh all` and fix `commands.env` if anything is off.
- Watch out: `internal/engine/pool.go` has shown CRLF-only diffs on the user's Windows machine. The kit's
  `.gitattributes` forces LF for `.claude/**/*.sh` only; do not add a repo-wide `eol` rule without asking.
- Manual steps the user does by hand, never Claude: `git filter-repo` purge of `agent_instructions/` from history,
  reword old commits, `depends_on` for the ollama sidecar in compose, push the image to Docker Hub.

## Log
- 2026-10-21 claude-kit 1.4.0 installed into Primus on `ft/dev`; roadmap rebuilt from the repo state at `6e20275`
