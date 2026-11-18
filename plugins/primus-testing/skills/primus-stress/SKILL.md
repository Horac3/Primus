---
name: primus-stress
description: Stress-test an API endpoint with Primus by stepping the request rate up until latency or errors break the budget, to find its breaking point and sustainable capacity. Use for stress testing, breaking-point or max-throughput questions, and capacity planning.
---

# Stress test with Primus

A stress test pushes past expected traffic on purpose to find where the API stops coping, and how it
fails. The result is "sustainable up to X req/s; at Y req/s p95 jumps to Z ms / requests drop / errors
start". Primus has no ramp stages yet (roadmap T3), so this skill runs a **ladder**: a series of fixed-rate
runs, each a step higher, stopping at the first step that fails.

Only against non-production environments, with the system's owner aware. A stress test can take a
shared environment down for everyone. Primus refuses these runs against `PRIMUS_PROTECTED_HOSTS`.

## 1. Agree the ladder

- Endpoint: one per ladder. Stress the endpoints that matter most, or the ones a load test showed as
  slowest.
- Start rate: about half the expected peak. Step: 10-25% of the peak. Max: the point where you would stop
  anyway (3-5x peak).
- Step length: 60 s or more (`step_seconds`), with a cooldown between steps (default 15 s).
- Pass criteria per step: `max_p95_millis` and `max_error_rate` from the API's budget. A step also fails
  if Primus has to drop requests because the in-flight cap is full, which means the server could not
  keep up.

## 2. Run the ladder on the server

Copy `scripts/stress-ladder.sh` into the server's Primus directory next to `./primus`, then start it with
`primus-remote`'s runner so it survives disconnects:

```sh
scp scripts/stress-ladder.sh <host>:~/primus-tests/ && ssh <host> 'chmod +x ~/primus-tests/stress-ladder.sh'
# request_id start step max step_seconds max_p95_ms max_error_rate
<primus-remote>/scripts/remote-run.sh <host> stress-orders --exec stress-ladder.sh <endpoint id> 50 25 400 60 500 0.01
<primus-remote>/scripts/remote-status.sh <host> stress-orders
```

Optional settings go in `primus.conf` on the server (it is loaded before the script): `CONCURRENCY`
(in-flight cap, default 200, raise it for high rates), `EXPECTED_STATUS` (for example `200`),
`COOLDOWN` (seconds), `DB`.

The script writes one config per step (`configs/stress-<stamp>-<rate>.json`), each step's reports and
`results/stress-<stamp>/ladder.csv`:

```
rate,achieved_rate,dropped,p95_ms,error_rate,outcome
50,50,0,42,0,pass
75,75,0,61,0,pass
100,88.3,185,2055,0,fail_rate_not_sustained
```

It prints `capacity: last passing step <rate> req/s`.

## 3. Interpret

- **Sustainable capacity** = the last passing step. Quote it with its p95 and error rate.
- **How it broke** matters as much as where:
  - `fail_rate_not_sustained` (dropped > 0): the server queued, and requests piled up past the in-flight cap.
  - p95 over budget with no errors: graceful slowdown.
  - Errors: look at `status_counts` in the failing step's JSON. 429 means a rate limiter, 503 shedding,
    5xx crashes or timeouts, and connection errors mean exhaustion.
- Re-run the failing step once to separate a real limit from noise before reporting it.
- After a failure, run a short low-rate check (the smoke config) to confirm the API recovered. A system
  that does not recover is the most important finding.

Use `primus-report` to write it up, and plot `ladder.csv` (rate vs p95 and error rate) if a chart helps.
