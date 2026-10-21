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
- [x] 10. LLM: query run history in natural language (FR-HIST-01..04, NFR-HIST-05, D-014).
- [x] 12. Downloadable test reports and insights (FR-REPORT-01..05, D-015).
- [x] 13. Credential provisioning (FR-PROV-01..07, D-016). Built, reviewed, committed; `scripts/race.sh` green. (`cd8f22d`..`bd616d5`)
- [x] 14. Refactor: split `internal/web/web.go` and `internal/engine/engine.go` by concern. (`b023f67`, `834c52a`, `ad970a0`)
- [x] 11. Dev tooling, last in the roadmap: local commit-message assistant ("meta") under `tools/`. Ollama only, rules
  fallback, not in the binary or image. FR-META-01..04. (`f7f8694`..`b0b87cb`)
- [x] C1. Collections: schema, migration, create/rename/base URL, delete collection, clear history — FR-COLL-02..06 (D-017) (`63d529a`..`7bd3fb0`)
- [x] C2. JSONPlaceholder seed, seeder with tombstone, Restore examples, `docs/collections.md` — FR-SEED-01..05
- [ ] C3. Grouped sidebar, collection page, import into a collection, run all — FR-COLL-01, 07, 08, FR-REP-03
- [ ] C4. Per-endpoint report with trend table and the key — FR-REP-01, 02
- [ ] C5. Richer insights: fixed sections, grounded Ollama output, rules fallback, previous-run comparison — FR-INS-01..03

## Current step
Step C3: Grouped sidebar, collection page, import into a collection, run all
Requirements: FR-COLL-01, 07, 08, FR-REP-03
Status: not started (C2 done)

## Handoff notes
- Done (C2, FR-SEED-01..05, FR-COLL-06 restore, `a036b32`..`8884eba`): `Notes` on RequestDefinition (`TestSaveAndGetRequestDefinition_KeepsNotes`);
  `store.SeedCollection` in one immediate tx (`TestSeedCollection_*`, incl. moved-endpoint restore); `internal/seed` (21 endpoints, `TestLoad_*`, `TestEnsure_*`);
  `seed.Ensure` at server start only; `POST /collections/restore` + button (`TestRestoreExamples_*`); Notes textarea in builder (`notes_test.go`); `docs/collections.md`.
  Reviewed; 2 blocking findings fixed. check.sh and `scripts/race.sh` green before the review fixes (fixes touch no goroutine code).
- Next action: C3. Grouped sidebar (by collection), collection page, import into a collection, run all (FR-COLL-01, 07, 08, FR-REP-03). Read those IDs first.
- Uncommitted: none, except untracked stray `internal/store/collections.go.new` (user must delete it by hand).
- Decisions: `Ensure` also seeds existing installs on upgrade (spec says fresh install); restore handler takes no `scopeGuard` (insert-only, serialized by the immediate tx);
  seeded rows keep fixed IDs `seed-jp-<slug>`; on restore an existing row with a seed ID (user moved it) is left where it is.
- Watch out: the builder does NOT load saved test configs (form defaults 5/50, no assertions), so seeded configs only matter for "run all" (C3, FR-COLL-07 must use them);
  docs/collections.md says so. Commit `ec44a4c` uses scope `seed`, not in the CLAUDE.md list (history not rewritten). CLAUDE.md layout line and docs/architecture.md lack `internal/seed`.
  Collections panel Restore button not visually checked (no Playwright). Earlier C1 nits still open (see git log `ebc704c`).
- Run /remap: new package `internal/seed`.
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
- 2026-10-21 step 10 done (`6d93009`..`3304a33`)
- 2026-10-21 step 11 requirements written, step moved last, architecture remapped (`3ed6303`)
- 2026-10-21 step 12 done (`a39cfc6`..`9702601`)
- 2026-10-21 step 13 partial (`cd8f22d`..`bd616d5`; race run pending)
- 2026-10-21 step 13 done (race.sh green; `cd8f22d`..`bd616d5`)
- 2026-10-21 step 14 done (`b023f67`, `834c52a`, `ad970a0`)
- 2026-10-21 step 11 done (`f7f8694`..`b0b87cb`)
- 2026-10-21 step C1 done (`63d529a`..`7bd3fb0`)
- 2026-10-21 step C2 done (`a036b32`..`8884eba`)
