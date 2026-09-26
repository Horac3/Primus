# Phase 3 — test engine (load + burst + assertions)

## Goal

The core of the whole tool: given a `RequestDefinition` and a
`TestConfig`, actually fire the requests, aggregate results, and
produce a pass/fail `Run`. This phase merges what were originally
three separate phases (load engine, burst mode, assertions) into
one, since burst is a config variant of the same engine and
assertions are a thin layer on the same aggregator. Time is short —
this phase is the one to spend the most of it on.

## Scope

1. `internal/engine`:
   - `RunLoad(ctx context.Context, req model.RequestDefinition, cfg model.TestConfig) (model.Run, error)`
     — worker-pool pattern: fixed `cfg.Concurrency` workers pulling
     from a job channel of size `cfg.TotalRequests`, each doing one
     HTTP call and emitting a `Result{StatusCode, Latency, Err}` on
     a results channel.
   - `RunBurst(ctx context.Context, req model.RequestDefinition, cfg model.TestConfig) (model.Run, error)`
     — same worker pool, but jobs are released in pulses per
     `cfg.BurstPattern.Count` then idle `cfg.BurstPattern.Idle`,
     repeated until `cfg.TotalRequests` is exhausted. Share the
     worker/dispatch code with `RunLoad` — a burst is a load test
     with a paced job feeder, not a separate execution path.
   - A single aggregator goroutine consumes the results channel
     (don't lock a shared struct from every worker) and produces
     `Summary`: per-status-code counts, error count, p50/p95/p99
     latency (sort samples, compute percentiles), total elapsed.
   - `http.Client` built via a tuned `http.Transport`
     (`MaxIdleConnsPerHost` and `MaxIdleConns` scaled to
     `cfg.Concurrency`, sane `IdleConnTimeout`) — a default
     `http.Client` will bottleneck on connection reuse before the
     target does, and the whole point is measuring the target, not
     your own client.
   - Dynamic param resolution: for each job, resolve any `ParamSpec`
     marked `Dynamic` (random int/string/sequential) into the actual
     request — this is what makes `petId` vary per request rather
     than hammering the same URL 10,000 times.
   - Respect `ctx` cancellation — an in-flight run must stop cleanly
     when the caller cancels, not drain to completion.

2. Assertions, evaluated once the run completes:
   - `ExpectedStatus`: any response code not in this list counts
     toward `Summary.ErrorCount`, even if the HTTP call itself
     succeeded — a clean 404 you didn't expect is still a failure
     for the assertion's purposes, distinct from a transport error.
   - `MaxErrorRate`, `MaxP95Millis`: compared against the computed
     summary.
   - `Run.Passed` is true only if every configured assertion holds.
     A `TestConfig` with no assertions set always passes (assertions
     are opt-in, not a hidden default threshold).

3. Persist the finished `Run` via Phase 1's `store.SaveRun`.

## Follow the standing rules

TDD/AAA per testing.md is especially important here: concurrency
bugs are exactly the kind of thing that look fine and aren't. Write
tests against a local `httptest.Server` (never a real network call)
that can simulate slow responses, error responses, and a controllable
delay so you can assert real concurrency behavior (e.g. "with
concurrency=5 and 20 slow jobs, no more than 5 are in flight at
once") rather than just "it ran". Session-logging.md and
git-commits.md apply as before — this phase's commit message should
call out that it merges the original load/burst/assertions phases,
so the git history stays honest about the scope change.

## Acceptance conditions (must ALL be true before phase 3 is done)

- [ ] `RunLoad` test against `httptest.Server`: asserts max observed
      concurrency never exceeds `cfg.Concurrency` (use a counter +
      mutex in the test server handler, not in engine code).
- [ ] `RunBurst` test: asserts requests arrive in the expected pulse
      pattern (count per burst, idle gap observed within a tolerance
      — timing tests need slack, don't assert exact millisecond
      timing).
- [ ] Percentile calculation has a unit test with a known input
      slice and known expected p50/p95/p99.
- [ ] Assertion evaluation has table-driven tests covering: passes
      when all thresholds met, fails on error rate, fails on p95,
      fails on unexpected status code, passes with no assertions
      configured.
- [ ] Dynamic param resolution (random int range) has a test
      confirming values vary across jobs and stay within bounds.
- [ ] Context cancellation test: cancel mid-run, confirm the run
      stops promptly rather than completing all `TotalRequests`.
- [ ] `go test ./... -race` passes — this phase is where a missed
      `-race` failure is most likely and most costly to find later.
- [ ] `go build ./...` succeeds.
- [ ] bob_sessions/ screenshot for this phase exists.
- [ ] One git commit exists for this phase, formatted per
      git-commits.md, noting the merge of load/burst/assertions.

Do not begin Phase 4 (minimal htmx UI) until every box above is
checked.
