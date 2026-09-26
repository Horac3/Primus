# Phase 4 — minimal htmx UI

## Goal

A thin web layer over Phases 1-3: import a spec, pick a request,
configure and launch a run, see the result. No live SSE streaming
(that's roadmap-only per the trimmed plan) — a simple poll-refresh
result page is enough to prove the engine works end to end.

## Scope

1. `internal/web`:
   - `NewServer(db *sql.DB) *http.ServeMux` (or your preferred
     router) wiring the handlers below. `cmd/primus` starts this on
     a configurable port.
   - `GET /` — dashboard: list of imported request definitions
     (from `store.ListRequestDefinitions`) and a form to import a
     new spec by URL.
   - `POST /import` — takes a URL, calls `importer.ImportFromURL`,
     persists results via `store.SaveRequestDefinition`, returns an
     htmx-swappable fragment (the updated request list) — no full
     page reload.
   - `GET /requests/{id}/run` — form to configure a `TestConfig`
     against that request: mode (load/burst), concurrency, total
     requests, burst count/idle (shown/hidden via a small bit of
     plain HTML, not JS, if feasible — e.g. two form sections with
     a mode radio and CSS or a no-JS `<details>` toggle), and
     assertion fields (expected status list, max p95, max error
     rate).
   - `POST /runs` — takes the submitted `TestConfig`, calls
     `engine.RunLoad` or `engine.RunBurst` based on mode, persists
     the `Run` via `store.SaveRun`, redirects (or htmx-swaps) to the
     run's result view. This call blocks until the run finishes —
     acceptable for MVP given no streaming; a large `TotalRequests`
     will make the request take a while, document that clearly on
     the form ("this will block until the run completes").
   - `GET /runs/{id}` — result view: status counts, p50/p95/p99,
     error rate, pass/fail badge, and the assertions that were
     checked. Use `hx-trigger="every 2s"` polling on this fragment
     only if you decide to make `/runs` non-blocking later — for
     this phase, since `/runs` blocks until done, polling isn't
     required; only add it if the run is dispatched in a goroutine
     instead. Pick one approach and keep it consistent — don't
     half-implement both.
   - `GET /runs` — list of past runs (id, config summary, pass/fail,
     timestamp) from `store.ListRuns`.

2. `web/templates`: plain `html/template`, htmx attributes
   (`hx-post`, `hx-target`, `hx-swap`) on forms and links, no
   client-side JS beyond what htmx itself needs (loaded from
   `cdnjs.cloudflare.com`, per the project's offline/self-hosted
   constraint — vendor the htmx script into `web/static` instead if
   you want zero external calls at runtime, your call given time
   left).

## Follow the standing rules

TDD/AAA per testing.md still applies to handler logic — use
`httptest.NewRequest`/`httptest.NewRecorder` to test each handler's
behavior (correct status codes, correct data in the response,
correct redirect/fragment on success) without needing a browser.
Full page rendering can be spot-checked manually given the time
constraint, but each handler's core logic gets a test. Session-
logging.md and git-commits.md apply as before.

## Acceptance conditions (must ALL be true before phase 4 is done)

- [ ] `POST /import` handler test: valid spec URL results in
      persisted request definitions and a non-error response.
- [ ] `POST /import` handler test: malformed/unreachable URL returns
      a clear error response, not a 500 with no context.
- [ ] `POST /runs` handler test: valid config results in a persisted
      `Run` with the correct `Passed` value for a known
      pass/fail scenario (use `httptest.Server` as the run's actual
      target, per Phase 3's testing approach).
- [ ] `GET /runs/{id}` handler test: returns 404 for an unknown ID,
      correct data for a known one.
- [ ] Manual end-to-end check: import the real Petstore spec through
      the running UI, launch a small load test (low concurrency/
      total, so it finishes fast) against a real endpoint, see a
      pass/fail result rendered.
- [ ] `go test ./... -race` passes.
- [ ] `go build ./...` succeeds.
- [ ] bob_sessions/ screenshot for this phase exists.
- [ ] One git commit exists for this phase, formatted per
      git-commits.md.

Do not begin Phase 5 (CLI mode) until every box above is checked.
