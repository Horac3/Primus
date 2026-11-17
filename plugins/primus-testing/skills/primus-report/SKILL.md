---
name: primus-report
description: Turn Primus results (run JSON, collection output, Markdown reports, series CSV, stress ladder.csv) into a clear test report with verdicts, latency, errors, comparisons and next steps. Use after any Primus run, or when someone asks what a Primus result means.
---

# Report Primus results

Primus results are aggregates: status counts, latency percentiles, error rate, a per-second time series.
They hold no request bodies, headers, tokens or credentials. This skill reads them and writes a report a
person can act on.

## Inputs (from `fetch-results.sh` or a `--report-dir`)

| File | Content |
|---|---|
| `<run id>.json` / stdout of `primus run` | `summary` (status_counts, error_count, p50/p95/p99_millis, error_rate, total_requests, cancelled; rate mode adds target_rate, achieved_rate, dropped), `passed`, `outcome`, optional `login` |
| `collection-*.json` / stdout of `primus run --collection` | per endpoint: `status` (passed, failed, no_assertions, skipped, error), `reason`, `run` |
| `<run id>.md` | Primus's own Markdown report: verdict, assertion checklist, metrics, insights |
| `<run id>.csv` | `elapsed_ms,avg_latency_ms,rps,error_rate` per interval |
| `ladder.csv` | stress ladder steps (see `primus-stress`) |

`primus list --collection <name>` on the server shows each endpoint's latest run if the files are gone.

## Read the verdict correctly

- `outcome` is the truth: `pass`, `fail` (an assertion failed), `fail_no_requests` (nothing completed,
  usually a network or base URL problem), `fail_no_logins` (credential pool logins all failed),
  `fail_rate_not_sustained` (rate mode dropped requests), `no_assertions` (nothing was checked, so it is not
  a pass).
- Exit codes: 0 pass, 1 fail or no assertions, 2 config error or refused by the protected host guard.
- `cancelled` requests were cut off by Stop or the duration deadline. They are not errors and not in the
  percentiles. Mention them if they are more than about 1% of requests.
- Primus asserts p95, error rate and status codes only. p99 and p50 are reported, not enforced.
- Error rate counts transport errors always, and unexpected statuses only when `expected_status` was set.
  Without it, a run full of 500s can show a 0% error rate. Check `status_counts`.

## Write the report

1. **Headline**: overall verdict, in one sentence, with the number that decided it ("FAILED: p95 640 ms
   against a 500 ms budget at 200 req/s on /orders").
2. **What ran**: test type, environment and base host (no query strings, no credentials), server location,
   date, mode, concurrency or rate, duration or count, identity (global auth / pool / none).
3. **Results table**, one row per endpoint or step: verdict, requests, rps (achieved vs target in rate
   mode), p50/p95/p99, error rate, top status codes.
4. **Time series**: from the CSV, note warm-up, spikes, a rising trend (soak) or recovery between pulses
   (burst). Summarise the shape in a sentence or two. Draw a chart only if the person wants one.
5. **Comparison** with the previous or baseline run where one exists (use `primus-regression`'s
   `compare_runs.py` for collections).
6. **Findings and next steps**: each finding with its evidence, then what to do (raise a limit,
   investigate an endpoint, re-run a borderline step). Separate facts from guesses.

Keep numbers exactly as Primus reported them, with units. Never invent a metric Primus did not measure
(CPU, memory, server-side latency). Ask the person for server-side graphs if the explanation needs them.

Where it goes: as a Markdown file next to the results, or as a document if the person wants to share it.
