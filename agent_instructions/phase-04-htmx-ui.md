# Phase 4 (v3) — sidebar, global auth override, async UI

Supersedes v2. Same sidebar/global-auth/inline-results direction, now
built against the unified async engine from the Phase 3 addendum
(read that first) and matching the UI mockups sent.

## Scope

### 1. Global auth, with per-endpoint override

- Global auth as designed in v2 (singleton `global_auth` row,
  `applyAuth` helper).
- **New, from the mockups:** a builder's header row can show
  `[GLOBAL]` as a placeholder value for `Authorization`, with an X to
  remove/override it for that one request. This means
  `RequestDefinition` needs an optional per-endpoint header override
  map (`Headers map[string]string`) that takes precedence over
  `applyAuth`'s injection when a header of the same name is present
  and not set to the `[GLOBAL]` sentinel — i.e., global auth is the
  default, silently overridable per endpoint, not mandatory.

### 2. Sidebar (unchanged from v2)

- `GET /sidebar-fragment`, `GET /requests/{id}`, manual "add
  endpoint," import still feeds the sidebar. Add a small sparkline
  per row if reasonable (last few runs' pass/fail or latency trend)
  — nice-to-have per the mockup, not blocking if it doesn't fit.

### 3. Async test flow (replaces v2's blocking `POST /runs`)

- `POST /probe` — unchanged, still a single synchronous request
  (fast enough to block on).
- `POST /runs` — starts a run via the Phase 3 addendum's registry,
  returns the "running" fragment immediately (numbered step 2
  becomes a progress/spinner state, Stop button visible and wired to
  `POST /runs/{id}/stop`).
- `GET /runs/{id}/status` — polled by `hx-trigger="every 1s"` on the
  running fragment; re-renders itself while running, swaps to the
  final result fragment once done.
- `POST /runs/{id}/stop` — calls the registry's cancel for that run.

### 4. Result rendering (matches the mockup)

- Verdict banner: PASS (green) / FAILED (red) / "completed — no
  assertions configured" (neutral) per the v1/v2 tri-state rule.
- Metric cards: p95 latency, requests/sec, error rate.
- Response-time histogram: bar chart from `Run.LatencyBuckets` —
  plain inline SVG bars are enough, no charting library needed for
  fixed-bucket data like this.
- Per-assertion checklist: one row per `Run.AssertionResults` entry,
  ✓/✗ and PASSED/FAILED per row, not just the overall verdict.

### 5. Mode config fields swap by mode

- Load: Concurrency + Duration (per the mockup — this is the
  hybrid's real consequence, load is now duration-driven by default
  in the UI, though the config format still accepts TotalRequests if
  you want to expose both as an advanced option later).
- Burst: Concurrency + BurstPattern (count/idle).
- Soak: Concurrency + Duration (longer defaults, otherwise identical
  fields to load — the UI can literally reuse the same form).
- Auth-probe: separate fields per its own earlier design (attempt
  count, rate, credentials) — not addressed by this phase, tracked
  separately.

## Follow the standing rules

TDD/AAA per testing.md, git-commits.md as before — note in the
commit message that this supersedes v2 and depends on the Phase 3
addendum landing first.

## Acceptance conditions

- [ ] Header override: an endpoint with `[GLOBAL]` removed from its
      Authorization row does NOT receive the global token; one that
      keeps it does. Test both states.
- [ ] `POST /runs` returns immediately (not blocked for the run's
      duration) — assert response time is small regardless of the
      configured Duration.
- [ ] `GET /runs/{id}/status` transitions from running-fragment to
      result-fragment once the underlying run completes.
- [ ] Stop button test: `POST /runs/{id}/stop` mid-run results in the
      next status poll showing a stopped/incomplete state, not stuck
      "running" forever.
- [ ] Histogram renders correct bar heights for a known
      `LatencyBuckets` input.
- [ ] Assertion checklist renders one row per configured assertion
      with correct pass/fail per row.
- [ ] Manual check: run a duration-based load test long enough to
      see the running state, click Stop, confirm it actually stops
      and the UI reflects that — not just that the button was
      clickable.
- [ ] `go test ./... -race` passes.
- [ ] `go build ./...` succeeds.
- [ ] One git commit, formatted per git-commits.md.

This phase depends on the Phase 3 addendum being done first — the
registry, unified `engine.Run`, per-assertion results, and histogram
buckets are all consumed here, not built here.