---
name: primus-regression
description: Regression-test an API with Primus - run the same collection before and after a change (or in CI on every release) and flag endpoints whose verdict, p95 or error rate got worse than a saved baseline. Use for regression testing, release gates, before/after comparisons, or "did this change make the API slower or break anything".
---

# Regression test with Primus

A regression test re-runs a fixed suite and compares it with a known-good baseline, so a bug fix or a new
feature cannot quietly break or slow down existing endpoints. In Primus the suite is a **collection** with
a saved config per endpoint, and the baseline is a saved collection run.

What it can catch today: changed status codes (via `expected_status`), latency regressions and error-rate
regressions. It cannot check response bodies (roadmap T1). Say so when someone expects contract checks.

## 1. Build the suite once

- One collection for the suite (`primus import ... --name "Orders API regression"`).
- A config for every endpoint: a modest count-based load run (for example 5 workers, 200 requests) with
  `expected_status` and the latency budget. Identical settings every time, or the comparison is
  meaningless. Use `--default-config`, then per-endpoint config files where the expected status differs.
- Read-only endpoints, or writes against a disposable environment that is reset before each run.

## 2. Record the baseline

On the known-good version, same server, same target environment:

```sh
./primus run --collection "Orders API regression" --report-dir results/regression-baseline --db primus.db \
  > results/regression-baseline.json
```

Keep `regression-baseline.json` (copy it into the repo next to the configs, for example
`perf/baselines/`). Re-record it only on purpose, when a slower result has been accepted, and say so in the
commit.

## 3. Run and compare after each change

```sh
./primus run --collection "Orders API regression" --report-dir results/regression-$(date +%F) --db primus.db \
  > results/regression-current.json
python3 scripts/compare_runs.py regression-baseline.json regression-current.json \
  --p95-tolerance 0.10 --min-ms 20 --error-tolerance 0.005
```

`compare_runs.py` (in this skill's `scripts/`, Python 3 standard library) matches endpoints by ID and marks
an endpoint REGRESSED when its verdict went from passed to anything else, its p95 grew by more than 10% *and*
more than 20 ms (both, so tiny endpoints do not flap), or its error rate grew by more than 0.5 points. New
and missing endpoints are listed. It prints a Markdown table and exits `1` on any regression, so it works as
a CI gate.

Latency is noisy: run the suite twice when a regression is borderline, and only compare runs from the same
server against the same environment. If the baseline was recorded at a different time of day or on a
different server, say so next to the result.

## 4. In CI

Pipeline steps: deploy to staging, `primus run --collection` (exit 1 or 2 fails the job on its own), then
`compare_runs.py` against the committed baseline (exit 1 fails the job on a regression). Store the
`--report-dir` output as build artifacts. Run it on the remote server via SSH if the CI runners are far
from the target (see `primus-remote`).
