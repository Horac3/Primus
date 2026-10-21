---
paths:
  - "internal/engine/**"
  - "internal/web/registry.go"
  - "cmd/primus/run.go"
---

# Engine and run lifecycle (Primus)

- Every goroutine has a clear exit path and an owner that waits for it. Look for channels nobody drains or
  closes and `WaitGroup` Add/Done mismatches.
- Anything read by one goroutine and written by another is atomic, mutex-guarded, or owned by a single goroutine
  (the aggregator owns the counters). Snapshots handed to readers are copies.
- A request that fails because the **run** context was cancelled is `Cancelled`, not an error. Decide by checking the
  run context (`ctx.Err() != nil`), not the error type: a per-request timeout is still an error.
- The verdict function is the single source of truth for PASS/FAILED. Zero completed requests is never PASS.
  The CLI exit code (0 pass, 1 failed, 2 usage) must come from the same verdict.
- Watch races at the edges: cancel racing natural completion, a result persisted twice, counters touched after the
  final summary is taken, a registry entry read after removal.
- Login phase (credential pools) runs before measurement and is reported separately. A cancel during login is not
  a failed login. Shared-account re-login on 401 is coordinated so one worker logs in and the rest reuse its token.
- Run `scripts/race.sh` before calling engine work done, and ask `concurrency-reviewer` to review the diff.
