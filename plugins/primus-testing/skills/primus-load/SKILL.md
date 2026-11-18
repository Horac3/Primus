---
name: primus-load
description: Load-test an API endpoint with Primus to measure latency and capacity at an expected traffic level, using fixed-rate (requests/sec) or worker-based load with p95 and error-rate assertions. Use for load testing, capacity checks, "can it handle N requests per second", or performance budgets.
---

# Load test with Primus

A load test runs the traffic you expect (or a bit more) and checks the API stays inside its latency
and error budgets. Its result is a statement like "at 200 req/s, p95 is 180 ms and errors are 0.02%".
To find where it breaks, use `primus-stress`; for slow degradation, `primus-soak`.

Run it on the server with `primus-remote`, against a non-production environment. Load runs are heavy:
Primus refuses them against hosts in `PRIMUS_PROTECTED_HOSTS` unless the config names the host in
`allow_protected_host`. Only add that when the person explicitly asks to load that production host.

## 1. Pick the mode

- **`rate` (open loop), the default for capacity questions.** Requests go out at `target_rate` per
  second whatever the server does, the way real users arrive. Latency counts from the scheduled send
  time, so a slow server cannot hide its queueing. Any dropped request fails the run.
- **`load` (closed loop).** `concurrency` workers each send, wait, send again. Throughput is
  concurrency / latency, so a slower server simply receives less traffic. Use it to model a fixed
  number of busy clients (batch jobs, a worker pool). Never describe concurrency as a request rate.

## 2. Size it

Ask for the target traffic (peak req/s from monitoring, or an SLA). With none, run 3 rates (for example
50%, 100% and 150% of a guessed peak) and say the numbers are guesses.

For `rate` mode set `concurrency` (the in-flight cap) to at least `target_rate x expected p95 seconds x 2`.
For example, 200 req/s at a 0.25 s p95 needs at least 100. Too low a cap drops requests and fails the run
for the wrong reason.

Duration: at least 60 s per level so the p95 settles, plus a short warm-up run first (caches, JIT,
connection pools). `duration` is in **nanoseconds**: 60 s = `60000000000`.

## 3. Configs

`configs/load-orders-200rps.json`:

```json
{
  "id": "cfg-load-orders-200rps",
  "request_id": "<endpoint id from primus list>",
  "mode": "rate",
  "target_rate": 200,
  "concurrency": 100,
  "duration": 120000000000,
  "assertions": {
    "expected_status": [200],
    "max_p95_millis": 300,
    "max_error_rate": 0.01
  }
}
```

Assertions come from the API's budget: `max_p95_millis` for latency, `max_error_rate` as a fraction
(0.01 = 1%). Primus asserts p95 only. It reports p99 in the summary, so quote p99 in the report but do
not claim it was enforced.

Bodies: for POST/PUT, the endpoint's body template is sent every time. To vary it, add a
`payload_pool` (a JSON array of bodies, cycled in order). Writes create real records, so agree a clean-up
plan first.

Many users: to send as many distinct accounts instead of one token, activate a credential pool (see
`primus-auth`).

## 4. Run

```sh
./primus run configs/load-orders-warmup.json --db primus.db           # 30 s at a low rate, ignore its verdict
./primus run configs/load-orders-200rps.json --report-dir results/load-orders --db primus.db
```

For runs over a few minutes use `scripts/remote-run.sh` from `primus-remote`. Run one level at a time,
with a pause of 30 s or more between levels.

## 5. Report

For each level give target vs achieved rate, dropped, p50/p95/p99, error rate, status counts and the
verdict, plus the server's location relative to the target. Name the highest level that passed. Use
`primus-report` for the write-up and the time-series CSV (latency, rps and error rate per second) to spot
warm-up effects or mid-run spikes.
