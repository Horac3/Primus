# Phase 3 addendum — unified async engine

Supersedes parts of the original Phase 3. `RunLoad`, `RunBurst`, and
the not-yet-built `RunSoak` collapse into one dispatch loop. Every
mode now runs asynchronously via a run registry, polled by the UI —
not blocking, not full SSE streaming.

## Why

The UI design needs two things the original blocking model can't
give: a Duration-based stop condition on any mode (not just soak),
and a working Stop button. Both require the run to happen off the
request-handling goroutine. Collapsing three run functions into one
turns out to be less code than keeping them separate, not more.

## Scope

### 1. Unified termination

```go
type TestConfig struct {
    Mode          string // load, burst, soak
    Concurrency   int
    TotalRequests int           // set this OR Duration, mutually exclusive
    Duration      time.Duration
    BurstPattern  *BurstPattern // only used when Mode == "burst"
    Assertions    Assertions
}
```

- Validate at config-parse time that exactly one of `TotalRequests`
  or `Duration` is set (not both, not neither) — reject otherwise
  with a clear error, don't silently pick a default.
- One `stopPredicate(cfg, start time.Time, sent *atomic.Int64) bool`:
  duration-based if `cfg.Duration > 0`, count-based otherwise.

### 2. One dispatch loop, pluggable job feeder

- `engine.Run(ctx, req, cfg) (model.Run, error)` replaces
  `RunLoad`/`RunBurst`/`RunSoak`. Internally: a job feeder function
  differs only for burst (paced pulses per `BurstPattern`) vs.
  load/soak (steady feed) — everything else (worker pool, tuned
  `http.Transport`, aggregator, `applyAuth`) is shared, unchanged
  from the original Phase 3 build.
- `ctx` cancellation must stop the run promptly regardless of which
  stop condition would otherwise apply — this is what the Stop
  button ultimately calls.

### 3. Run registry (async execution + polling)

- In-memory registry: `runID -> *runHandle{cancel context.CancelFunc,
  status string, result *model.Run}`. `sync.Map` or a mutex-guarded
  map — pick one, keep it simple, this doesn't need to survive a
  process restart (finished runs are already persisted to SQLite via
  `store.SaveRun`; the registry only tracks in-flight ones).
- Starting a run: create a cancellable context, store the handle,
  launch `engine.Run` in a goroutine, return immediately with the
  run ID and a "running" fragment.
- Polling a run: if still running, re-render the same "running"
  fragment (so `hx-trigger="every 1s"` keeps polling itself); once
  done, render the final result fragment instead — polling stops
  naturally because the fragment no longer has a poll trigger.
- Stopping a run: look up the handle, call its `cancel()`. Guard the
  race where Stop is called at the same instant the run finishes
  naturally — use a `sync.Once` (or an atomic compare-and-swap on
  status) around the "mark as done and persist" step, so cancellation
  arriving a moment too late is a harmless no-op, not a double-write
  or a panic.
- A finished (cancelled or completed) run is removed from the
  registry after its result has been polled once, or after a short
  TTL — don't let the registry grow unbounded across a long session.

### 4. Per-assertion results (needed for the UI's assertion checklist)

- `model.Run.AssertionResults []AssertionResult` where
  `AssertionResult{Name string, Passed bool}` — one entry per
  configured assertion ("Status code: 200", "Latency (p95) < 200ms",
  "Error rate < 1%"), not just the overall `Passed` bool. The overall
  `Passed` is true only if every entry in this slice is true (plus
  the existing "zero successes always fails" rule from the v1 fix).

### 5. Latency histogram bucketing (needed for the response-time chart)

- Aggregator gains bucketed counts alongside p50/p95/p99 — fixed
  bucket edges are fine for a first pass (e.g. 0-50-100-150-200-
  300-400-500-600ms, matching the mockup's axis), exposed as
  `Run.LatencyBuckets []int` aligned to those edges.

## Follow the standing rules

TDD/AAA per testing.md. The cancel-vs-natural-completion race gets
its own explicit test, not just "seems to work" — fire a run against
a slow `httptest.Server`, call cancel and let it finish at
approximately the same time (a small sleep to line them up), assert
no panic and exactly one final state written. git-commits.md as
before.

## Acceptance conditions

- [ ] Config validation rejects both-set and neither-set
      TotalRequests/Duration.
- [ ] `engine.Run` with `Duration` set stops at approximately the
      deadline (tolerance-based assertion, not exact).
- [ ] `engine.Run` with `TotalRequests` set behaves identically to
      the original `RunLoad`/`RunBurst` (regression check — existing
      Phase 3 tests should still pass against the unified function).
- [ ] Run registry: start a run, poll while running (get the running
      fragment), poll after completion (get the result fragment) —
      test the state transition directly, not just the HTTP surface.
- [ ] Stop test: cancel mid-run, confirm the run stops promptly and
      its stored result reflects a cancelled/incomplete state.
- [ ] Race test: cancel fired at approximately natural completion,
      no panic, no duplicate `store.SaveRun` call.
- [ ] `AssertionResult` entries match expected pass/fail per
      configured assertion, table-driven across several scenarios.
- [ ] Histogram bucket counts sum to the total request count, for a
      known input distribution.
- [ ] `go test ./... -race` passes — this phase is concurrency-heavy,
      treat a race-detector failure as blocking, not cosmetic.
- [ ] `go build ./...` succeeds.
- [ ] One git commit, formatted per git-commits.md.

CLI mode (Phase 5) calls `engine.Run` directly and can stay
synchronous — it just calls the same function and waits, no registry
needed there since there's no UI to poll. Confirm it still compiles
against the new `TestConfig`/`Run` shape.