# Phase 4 (v2) — sidebar, global auth, soak mode

This replaces the v1 single-endpoint rework, which did not finish.
Same direction (one page, no forced navigation), plus what's been
decided since: authentication is global rather than per-endpoint,
endpoints are browsed via a sidebar rather than one at a time with no
list, and soak joins load/burst/auth-probe as a fourth test mode.

UI designs are coming separately and will refine layout/styling —
this phase should get the functional pieces (routes, data model,
fragments) working now so visual polish can drop in later without
backend rework. Don't over-invest in CSS/layout precision yet.

## Goal

One page: a persistent auth bar at the top, a sidebar of every
defined endpoint (imported or manually added) on the left, and a
main panel that loads the selected endpoint's builder, connection
test, mode selector, and results — all inline, no page navigation.

## Scope

### 1. Global auth (replaces per-endpoint `AuthConfig`)

- `internal/store`: `global_auth` table, exactly one row (id=1,
  enforced via `CHECK (id = 1)`), columns for auth_type
  (none/bearer/api_key/basic), token, header_name (for api_key),
  username/password (for basic).
- `GetGlobalAuth(db) (model.AuthConfig, error)` and
  `SaveGlobalAuth(db, model.AuthConfig) error`.
- Remove `Auth` from `model.RequestDefinition` — auth is no longer a
  property of an endpoint.
- `internal/engine`: one `applyAuth(req *http.Request, auth
  model.AuthConfig)` helper, called at dispatch time in every mode
  (probe, load, burst, auth-probe, soak) — auth is fetched fresh from
  `GetGlobalAuth` right before firing requests, not baked into a
  saved definition.
- `POST /auth` — saves global auth config, returns the auth-bar
  fragment re-rendered (e.g. "Bearer ••••1234" or "Not set").
- `GET /auth-fragment` — the same fragment, for initial page load.

### 2. Sidebar

- `GET /sidebar-fragment` — lists every `RequestDefinition` (from
  manual entry or the existing importer), name/method/path per row.
- `GET /requests/{id}` — returns the main-panel fragment: that
  endpoint's builder pre-filled (method, URL, body — no auth field,
  it's global now), a "Test connection" button, and an empty results
  area.
- Manual "add new endpoint" affordance in the sidebar opens a blank
  builder in the main panel (same fragment, no pre-fill).
- Existing `POST /import` (URL or file) still works as designed
  earlier, but now returns/refreshes the sidebar-fragment plus loads
  the first imported endpoint into the main panel, rather than
  presenting its own separate list screen.

### 3. Test flow in the main panel (per selected endpoint)

- `POST /probe` — fires one request using the endpoint's builder
  fields plus current global auth. Returns status code, response
  time, truncated body preview, success flag. On success, includes
  the mode selector (load/burst/auth-probe/soak) + relevant config
  fields, appended into the same results area.
- `POST /runs` — takes the endpoint + chosen `TestConfig`, runs the
  matching engine function, persists the `Run`, returns the result
  card inline in the same results area. No redirect, no separate
  results page as the primary path (`GET /runs/{id}` can still exist
  for a shareable link).

### 4. Soak mode (new)

- `model.TestConfig` gains `Duration time.Duration`, used only when
  `Mode == "soak"` (load/burst keep using `TotalRequests`).
- `engine.RunSoak(ctx, req, cfg)` — same worker-pool/dispatch core as
  `RunLoad`, but jobs feed continuously until a deadline
  (`context.WithDeadline(ctx, time.Now().Add(cfg.Duration))`) instead
  of draining a fixed-size channel. For this phase, keep demo-scale
  durations (tens of seconds to a couple minutes) — true multi-hour
  soak with trend graphing is a later concern, not this phase's.
- Mode selector in the UI: soak's config fields are
  concurrency + duration, not concurrency + total-requests — the
  form should swap which fields show based on the selected mode
  (plain conditional HTML, no need for JS beyond what's already used
  for the mode dropdown, if anything).

### 5. Carry forward from v1 (still required, not yet done)

- The pass/fail badge fix: a run with zero successful requests must
  never render green "PASS" regardless of configured assertions; zero
  assertions configured but at least one success renders a neutral
  "completed — no assertions configured" state, distinct from a real
  threshold-met pass.

## Follow the standing rules

TDD/AAA per testing.md still applies — this is real logic (auth
singleton enforcement, soak's deadline-based dispatch, the pass/fail
tri-state), not just templates. git-commits.md still applies: one
commit for this phase, message noting it supersedes the unfinished
v1 and adds global auth / sidebar / soak.

## Acceptance conditions (must ALL be true before phase 4 is done)

- [ ] `SaveGlobalAuth` then `GetGlobalAuth` round-trips correctly;
      only one row ever exists regardless of how many times it's
      saved (upsert, not insert).
- [ ] A request fired via `applyAuth` carries the correct header for
      each auth type (bearer/api_key/basic), verified against an
      `httptest.Server` that asserts on the header it received.
- [ ] Switching the sidebar selection to a different endpoint does
      not require re-entering auth — global auth applies automatically.
- [ ] `RunSoak` test: with a short deadline (e.g. 2s) and a fast
      target, confirms the run stops at approximately the deadline
      rather than running a fixed count — assert on elapsed time
      with reasonable tolerance, not exact milliseconds.
- [ ] `POST /runs` test: 100% failing requests → `Passed == false`
      regardless of configured assertions (the carried-over bug fix).
- [ ] `POST /runs` test: zero assertions configured, at least one
      success → distinct "no assertions configured" state, not a
      plain green pass.
- [ ] Manual check: full flow — define or import endpoints, see them
      in the sidebar, set global auth once, click between two
      different endpoints and confirm both authenticate correctly,
      run a load test and a soak test against the same endpoint from
      the same panel, see results inline both times.
- [ ] `go test ./... -race` passes.
- [ ] `go build ./...` succeeds.
- [ ] One git commit exists for this phase, formatted per
      git-commits.md.

Do not begin Docker re-verification (Phase 6) until every box above
is checked — the data model changed (auth moved off
`RequestDefinition`), so re-run the container persistence check
afterward even though Phase 6 itself was previously marked done.