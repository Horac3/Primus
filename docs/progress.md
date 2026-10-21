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
- [ ] C2. JSONPlaceholder seed, seeder with tombstone, Restore examples, `docs/collections.md` — FR-SEED-01..05
- [ ] C3. Grouped sidebar, collection page, import into a collection, run all — FR-COLL-01, 07, 08, FR-REP-03
- [ ] C4. Per-endpoint report with trend table and the key — FR-REP-01, 02
- [ ] C5. Richer insights: fixed sections, grounded Ollama output, rules fallback, previous-run comparison — FR-INS-01..03

## Current step
Step C2: JSONPlaceholder seed, seeder with tombstone, Restore examples, `docs/collections.md`
Requirements: FR-SEED-01..05 (and the restore half of FR-COLL-06)
Status: not started (C1 done)

## Handoff notes
- Done (C1, FR-COLL-02..06, `63d529a`..`7bd3fb0`): collections + seed_tombstones tables, guarded `ALTER requests ADD collection_id`, "My endpoints"
  migration (`TestMigrate_*`); collection CRUD/validation; `model.ExpandBaseURL`/`store.ResolveURL` used by web probe/run and CLI;
  `DeleteCollection`/`ClearHistory` under `BEGIN IMMEDIATE` (`inImmediateTx`); registry `ActiveRequestIDs` + `scopeGuard`
  (run start, import, delete, clear); Collections panel (`/collections`, `/history/clear`), collection select, clear buttons; docs in load/cli/history.md.
  Reviewed (reviewer + concurrency-reviewer), fixes committed. check.sh and `scripts/race.sh` green.
- Next action: C2. Create `internal/seed` (embedded JSON, ~20 JSONPlaceholder endpoints with `{{base_url}}` URLs, saved configs, assertions, notes).
  Seeder sets `collections.seed_id/seed_version`, skips when `store.Tombstoned(db, seedID)` or the collection exists; "Restore examples" must clear the tombstone and
  re-seed ("already present" if it exists). Add `docs/collections.md` to the embed list AND `order` in `docs/docs.go`, plus the docs test list.
- Uncommitted: none, except untracked stray `internal/store/collections.go.new` (my abandoned draft; user must delete it by hand).
- Decisions: tombstone written by `DeleteCollection` in C1 (restore is C2); base URL rejects userinfo/query/fragment; `{{base_url}}` only at URL start.
- Watch out: ui-checker could not run (no Playwright); collections panel/new controls are NOT visually checked, and there is still no mobile breakpoint or `:focus-visible`.
  Not fixed (nits): `StartInsight` after `GetRun` is not under the guard (clear can leave an orphan insight); collection row counts go stale after a row-level clear;
  import-vs-delete race test is only a tripwire (passed before the guard). Sidebar is still a flat list (C3). CLI runs are invisible to the server's active-run check (documented).
  Remaining spec-vs-code notes for C3..C5: run has no endpoint field (join via test_configs, rule needed for several configs); `importer` returns no spec title (FR-COLL-08).
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
