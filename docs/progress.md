# Progress

State lives here, not in chat history. `/next` reads "Current step" and "Handoff notes";
`/handoff` rewrites them. Markers: `[ ]` todo · `[~]` in progress · `[x]` done.

## Roadmap
- [x] 1. Credential pools: types, storage, login phase, UI, smoke test (`848a90c`..`16afb8c`)
- [x] 2. Fix: a run with zero completed requests never passes (`3bc3962`)
- [x] 3. Fix: cancelled in-flight requests counted separately from errors (`64b7c95`, `6e20275`)
- [x] 4. Fix: flaky `TestProbe_SuccessfulTarget` (`LatencyMs <= 0` at `internal/engine/engine_phase4_test.go:152`).
  Reproduce at `-count=50`, assert `< 0` instead, no sleeps, and look for the same `> 0`-on-a-measured-duration
  pattern elsewhere. Spec: `agent_instructions/fix-verdict-and-flaky-probe.md` (Fix 2). (`d1326cf`)
- [x] 5. CI: add a test job (build, vet, test, then `scripts/race.sh`-equivalent `go test -race`) that runs on pushes
  and pull requests, and make the Docker publish job depend on it. NFR-CI-01.
- [x] 6. Per-field credential masking. FR-CRED-01..04. Spec: `agent_instructions/fix-credential-field-masking.md`.
- [x] 7. Docs and UI follow-ups from the setup check (README auth-probe wording was OK and is excluded):
  - `docs/credential-pools.md`: re-login on 401 is documented as off by default, but nothing says when to turn it on.
    A soak run with short-lived tokens 401s after expiry unless it is on. Add that guidance.
  - Analyzer default: `deploy/docker-compose.yml` sets `PRIMUS_ANALYZER=ollama` unconditionally. With the `ai` profile
    off, `NewAnalyzer` still builds the Ollama analyzer (no reachability check), so insights start labelled
    `ollama`/`qwen2.5:3b`, fail on the unresolvable `ollama` host, then fall back to rules (final label `rules`).
    The natural-language authoring and payload-generation UI stays enabled and returns 502 on use. Decide the default
    (rules unless the profile is on, or say in the UI that Ollama is unreachable). Leave `depends_on` to the user. (`975b829`, `611b4f8`)
- [x] 8. Auth-probe UI wiring: attempt count, rate and credential fields in the UI. FR-PROBE-01..04. (`1d9e95a`..`ed4ed09`)
- [x] 9. Open-loop fixed req/s mode. FR-RATE-01..04. Start the step with `/decide` on the dispatcher design
  (ticker-driven release, in-flight cap, `Dropped` accounting, latency from intended send time).
- [x] 9a. Fix: SQLite busy errors drop writes. `OpenDB` has no busy timeout or WAL, so a write that overlaps a read
  fails with `SQLITE_BUSY`; a dropped `CompleteInsight` leaves the insight stuck on "Generating...". D-013.
- [ ] 10. LLM: query run history in natural language. Needs requirements and a spec first.
- [ ] 11. LLM: commit-message assistant ("meta"). Needs requirements and a spec first.
- [ ] 12. Downloadable test reports and insights. Needs scoping.
- [ ] 13. Credential provisioning (sign-up before the run). Later and advanced; reuses the `Extraction` primitive.
  Needs scoping and a `/decide`.
- [ ] 14. Refactor: split `internal/web/web.go` and `internal/engine/engine.go` by concern. Do it as the first
  commit of whichever step next touches each file, not as a standalone big-bang change.

## Current step
Step 10: LLM: query run history in natural language
Requirements: none yet (needs requirements and a spec first)
Status: not started

## Handoff notes
- Done: step 9 race gate (full `scripts/race.sh` clean). Step 9a: `OpenDB` uses busy_timeout(5000) + WAL, `&`-joins
  pragmas onto a path with a query; `generateRunInsight` fails the insight if `CompleteInsight` errors. Tests:
  `TestCompleteInsight_SucceedsDuringOpenRead`, `TestCompleteInsight_WaitsForOtherWriter`,
  `TestOpenDB_PathWithQueryKeepsPragmas`, `TestRunInsightFailsWhenCompletionCannotBeSaved`.
  `TestRunInsightAsyncLifecycleAndReuse` (was 2/10 flaky) passed 10/10 under race.
- Next action: step 10 needs requirements and a spec before planning; ask the user for scope.
- Uncommitted: none.
- Decisions: D-013 (the user asked for D-009, already taken by credential masking).
- Watch out: C: had ~3.7GB free after the user cleaned up; Docker Desktop dies when C: fills. Tests OOM with the Go
  cache on C:; set GOCACHE/GOTMPDIR/TMP to `D:/.gocache-primus`. CRLF-only gofmt hits on untouched files:
  `internal/importer/importer.go`, `importer_test.go`, `internal/web/registry.go`, `registry_test.go`.
  No `ui-checker` screenshot of the rate fields yet. Rate docs live in `docs/load.md` (docs_test pins the page list).
- Step 14 partly done: engine.go split (b023f67); web.go split still pending.
- Run /remap: engine.go split into dispatch.go, aggregate.go, verdict.go; new rate.go.
- Manual steps the user does by hand, never Claude: `git filter-repo`, interactive rebase, compose `depends_on`,
  Docker Hub push, deleting `AGENTS.md`/`.bobignore`/`primus-scaffold-plan.md`/root `web/`, repo-wide `eol` rule.

## Log
- 2026-10-21 claude-kit 1.4.0 installed into Primus on `ft/dev`; roadmap rebuilt from the repo state at `6e20275`
- 2026-10-21 setup verified: `commands.env` created, `check.sh all` PASS, race run blocked (Docker down); roadmap reset to steps 4–14
- 2026-10-21 step 4 done (`d1326cf`)
- 2026-10-21 step 5 done (`09f025e`)
- 2026-10-21 step 6 done (`675da1d`)
- 2026-10-21 step 7 done (`611b4f8`)
- 2026-10-21 step 8 done (`ed4ed09`)
- 2026-10-21 step 9 partial (`be88791`)
- 2026-10-21 step 9 done (`855d841`)
- 2026-10-21 step 9a done (`e4ab16c`, `855d841`)
