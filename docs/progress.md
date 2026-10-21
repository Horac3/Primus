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
- [ ] C1. Collections: schema, migration, create/rename/base URL, delete collection, clear history — FR-COLL-02..06 (D-017)
- [ ] C2. JSONPlaceholder seed, seeder with tombstone, Restore examples, `docs/collections.md` — FR-SEED-01..05
- [ ] C3. Grouped sidebar, collection page, import into a collection, run all — FR-COLL-01, 07, 08, FR-REP-03
- [ ] C4. Per-endpoint report with trend table and the key — FR-REP-01, 02
- [ ] C5. Richer insights: fixed sections, grounded Ollama output, rules fallback, previous-run comparison — FR-INS-01..03

## Current step
C1. Collections: schema, migration, create/rename/base URL, delete collection, clear history (D-017).
Requirements: FR-COLL-02..06
Status: planning (steps 4–14 done; C1..C5 added after step 11)

## Handoff notes
- Planning done for C1..C5 (D-017, FR-COLL/SEED/REP/INS written; docs only, nothing built, nothing committed). Per-step notes
  are in the pasted spec "Collections, the JSONPlaceholder seed, per-endpoint reports, richer insights"; `/next` should
  plan C1 first. Spec-vs-code conflicts found:
  1. **Roadmap position:** the spec says "after the CI and masking steps"; both (5, 6) are done and the roadmap was
     complete, so C1..C5 sit at the end, after step 11. "Current step" is unchanged and still says roadmap complete; `/next` must pick C1.
  2. **URLs are stored whole and used verbatim.** `requests.url_template` is a full URL; `execRequest`
     (`internal/engine/dispatch.go:193`) uses `def.URLTemplate` as-is and `engine.Probe` goes through `execRequest`.
     `{{base_url}}` needs a resolution step before `execRequest` (it has no collection access). `{name}` path
     placeholders are a different syntax, already handled there. Also shown unresolved in `report.go:79`,
     `query_filter.go:225` (history search haystack), `authoring.go`, `runs.go:41`; decide whether those show the template or the resolved URL.
  3. **No collection concept anywhere.** `requests(id, name, method, url_template, payload)` has no collection column;
     `Migrate` is `CREATE TABLE IF NOT EXISTS` plus `migrateMaskedFields`, with no versioned migrations, so adding a column
     and the "My endpoints" backfill must be idempotent by hand (check `PRAGMA table_info`).
  4. **No foreign keys.** `test_configs.request_id`, `runs.test_config_id` and `insights.run_id` are plain TEXT, so
     "delete collection" cascades must be explicit DELETEs (endpoint -> configs -> runs -> insights) in one tx. A run's
     endpoint is reached only through its test config. Credential pools are not per-endpoint and stay untouched.
  5. **Active-run check lives in the web layer.** The registry (`internal/web/registry.go`) is keyed by run ID; the
     store cannot know about active runs, so handlers must collect active run IDs for the scope and pass them in (or check first), as the spec says.
  6. **Run has no endpoint or collection field**, so per-endpoint trend and "clear history for one endpoint" join through `test_configs`.
     An endpoint may have several configs; FR-COLL-07 "saved test config" and FR-REP-01 "last 10 runs" need a rule for which config(s) (suggest: latest config; runs of all its configs).
  7. **Sidebar** (`templates/sidebar.html`) is a flat `range .Endpoints` with `.Recent` dots, htmx buttons and inline
     styles; no mobile breakpoint or focus style exists (see Watch out), which the terminal-style notes require for new views.
  8. **Import** (`importer.ImportFromBytes/URL`) returns `[]RequestDefinition` only, with no spec title, so FR-COLL-08 needs the title returned too.
  9. **Docs embed:** `docs/docs.go` has an explicit embed list and `order`; `collections.md` goes in both (FR-SEED-05) and the docs test list.
  10. **Seed tests** need `{{base_url}}` (item 2) first, since `http://` hosts in `httptest` cannot be the seed's real base URL.
- Previous handoff (step 11), still true:
- Done (step 11, FR-META-01..04): `tools/meta` (`go run ./tools/meta`, README there): `Validate`, `RulesDraft`/`scopeForPath`,
  `TruncateDiff`, `OllamaGenerator`, read-only `RealGit`, `run`. Tests: `TestValidate_*`, `TestScopeForPath`, `TestRulesDraft_*`,
  `TestTruncateDiff_*`, `TestRun_*` (httptest Ollama, six fallback causes, empty diff calls no model, non-local URL refused),
  `TestRealGit_*` (index and HEAD unchanged), `TestBinaryDoesNotDependOnTools`, `TestImageDoesNotShipTools`.
  Reviewed (reviewer subagent); the blocking finding (empty bullet / empty `Tests:` accepted) and most minors fixed. check.sh green.
- Next action: none planned. The roadmap is finished; add new steps to the roadmap before the next `/next`.
- Uncommitted: `docs/progress.md` and `docs/architecture.md` until the handoff commit.
- Decisions: added commit scope `tools` (CLAUDE.md, FR-META-02); `tools/` mapped to it. `--allow-remote` is needed for a non-loopback Ollama URL.
- Not fixed from review: `ollama.go` sends the `Files:` list on top of the capped diff (unbounded with very many files, duplicated when
  truncated); commit `f81ee76` is typed `test(tools)` but also touches `.dockerignore` (history not rewritten).
- Not run: `scripts/race.sh` for step 11 (no goroutines of its own; only net/http).
- Not done / known: Stop can leave an in-flight sign-up created on the target but unsaved (documented in the docs page);
  `AppendCredentials` uses a deferred tx (BEGIN IMMEDIATE would avoid SQLITE_BUSY_SNAPSHOT); Stop racing the 1s poll can
  hide the summary; no test for two overlapping registry Start calls (only visible under -race).
- Watch out: `ui-checker` had no Playwright (no screenshots). Pre-existing global UI gaps, not fixed: no mobile `@media`
  breakpoint (sidebar 260px at 360px), no `:focus-visible` style, link/summary blue ~3.8:1. Never put `python`/`python3`
  in Bash here (Windows Store alias hangs). Set GOCACHE/GOTMPDIR/TMP to `D:/.gocache-primus`. CRLF-only gofmt hits on
  untouched files: `internal/importer/importer.go`, `importer_test.go`, `internal/web/registry_test.go`.
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
- 2026-10-21 step 10 done (`6d93009`..`3304a33`)
- 2026-10-21 step 11 requirements written, step moved last, architecture remapped (`3ed6303`)
- 2026-10-21 step 12 done (`a39cfc6`..`9702601`)
- 2026-10-21 step 13 partial (`cd8f22d`..`bd616d5`; race run pending)
- 2026-10-21 step 13 done (race.sh green; `cd8f22d`..`bd616d5`)
- 2026-10-21 step 14 done (`b023f67`, `834c52a`, `ad970a0`)
- 2026-10-21 step 11 done (`f7f8694`..`b0b87cb`)
