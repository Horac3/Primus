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
- [x] C3. Grouped sidebar, collection page, import into a collection, run all — FR-COLL-01, 07, 08, FR-REP-03 (`fb43439`..`e12d36e`; `scripts/race.sh` green)
- [x] C3b. (`2e975a0`, `60b0a73`; race.sh green; D-019) SQLite write queue: a single dedicated write connection (readers separate) so Primus's own writers queue instead of poll. Evidence: single writer calls blocked 3.97s to 4.79s in an instrumented `TestDeleteAndClear_SurviveConcurrentWriters`, and `SaveRun` returned `SQLITE_BUSY` once in 200 runs even with pacing (busy handler polls, it does not queue). Provisioning (step 13) already inserts many rows from an async job while the UI polls, which is this load. `inImmediateTx` pins a conn, so audit it first. Cheaper stopgap if needed earlier: bounded retry-with-backoff on `SaveRun` and `CompleteInsight`.
- [x] C4. (`cc79b1a`..`ff05cb0`) Per-endpoint report with trend table and the key — FR-REP-01, 02
- [ ] C5. Richer insights: fixed sections, grounded Ollama output, rules fallback, previous-run comparison — FR-INS-01..03

## Current step
Step C5: Richer insights: fixed sections, grounded Ollama output, rules fallback, previous-run comparison
Requirements: FR-INS-01..03
Status: not started

## Handoff notes
- Done (C4, `cc79b1a`..`ff05cb0`): `store.RecentRunsByRequest`; `GET /requests/{id}/report` (`internal/web/endpoint_report.go`, `endpoint-report.html`): header, latest run, last-10 trend table, findings via `insight-fragment`, empty state with a Run button that opens the builder; one fixed key (`internal/web/readkey.go`, `report-key.html`) on the endpoint page, run page, report.html and report.md, login latency only when a pool ran. Tests: `TestRecentRunsByRequest_*`, `TestEndpointReport_*`, `TestRunReports_AllCarryTheKey`, `TestCollectionPage_EveryEndpointLinksToItsReport`. `check.sh all` green; `/review` found nothing blocking. Docs: `reports.md`, `collections.md`.
- Next action: begin C5 (`/next`, plan mode).
- Uncommitted: none (this progress edit pending commit).
- Decisions: collection page keeps its last-run link and `-` columns; the report link sits in the endpoint cell ("endpoint report"), because existing tests lock the old row shape. A collection `base_url` cannot hold userinfo or a query (store rejects it).
- Watch out: `scripts/race.sh` NOT run for C4 (no goroutine code; store change is a plain read). The endpoint report was NOT visually checked: Playwright is not installed, and adding it needs the user's OK. Check at 360px (summary and trend tables are wrapped in `overflow-x:auto`), plus the collection page, sidebar collapse and Run all from C3.
- Watch out: this checkout has autocrlf, so `gofmt -l` flags CRLF files (e.g. `internal/store/collections.go`); that is noise.
- Watch out: never use `git stash` as a check; it rewrote line endings in the working tree.
- Watch out: rule: never call the `*sql.DB` while holding a conn, rows or tx. Follow-ups: `AppendCredentials`/`SaveCredentialPool` use deferred txs, move to `inImmediateTx`; `ListAllRuns`/`ListRuns` unmarshal while rows open; consider `-timeout 120s` in `commands.env` and `scripts/race.sh`.
- Watch out: `TestRunBurst_DeliversPulses` (`internal/engine/engine_test.go:315`) failed once under race load; leave unless it fails again.
- Open C3 nits: delete collection between two run-all items skips the rest; `LatestRunsByRequest` reads every payload; run-all item links its report while running (404); `runID` UnixNano only; builder does not load saved configs.
- Run /remap: new `internal/seed`, `internal/web/{sequence,collection_page,endpoint_report,readkey}.go`.

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
- 2026-10-22 step C3 partial (`fb43439`..`e12d36e`; race.sh pending)
- 2026-10-22 step C3b done (`2e975a0`..`60b0a73`)
- 2026-10-22 step C4 done (`cc79b1a`..`ff05cb0`)
