---
name: primus-spike-burst
description: Test how an API copes with sudden traffic - burst tests (pulses of requests with idle gaps, to check recovery between pulses) and spike tests (a sudden jump to a high rate between two normal-traffic runs). Use for burst, spike, flash-sale or thundering-herd questions.
---

# Burst and spike tests with Primus

Steady load never lets the API breathe, so it cannot show whether it **recovers**. These two tests can:

- **Burst**: pulses of N requests, idle gaps between them. Does latency return to normal after each
  pulse, or ratchet up pulse after pulse? Primus `burst` mode does this directly.
- **Spike**: normal traffic, then a sudden jump to several times that, then back to normal. Does the
  API survive the jump, and does the "after" look like the "before"? Primus has no ramp stages yet
  (roadmap T3), so a spike is three fixed-rate runs back to back.

Both are heavy: non-production only (Primus refuses them against `PRIMUS_PROTECTED_HOSTS`). Run them on
the server with `primus-remote`.

## Burst

```json
{
  "id": "cfg-burst-orders",
  "request_id": "<endpoint id>",
  "mode": "burst",
  "concurrency": 50,
  "total_requests": 1000,
  "burst_pattern": { "count": 100, "idle": 2000 },
  "assertions": { "expected_status": [200], "max_p95_millis": 800, "max_error_rate": 0.01 }
}
```

- `count` requests per pulse, `idle` milliseconds between pulses, `total_requests` across all pulses
  (here 10 pulses). `concurrency` caps how many of a pulse are in flight at once. Set it to `count` for
  a true simultaneous burst.
- Choose `count` from the burst you expect (a cron fan-out, a push notification wave). Choose `idle` to
  be a bit longer than the API should need to recover.
- Read recovery from the series CSV (`--report-dir`): average latency per second should drop back
  between pulses. A rising floor from pulse to pulse means queues or connection pools are not draining.

## Spike

Three `rate` runs, one after another, same endpoint:

| Run | target_rate | duration | Purpose |
|---|---|---|---|
| before | normal (e.g. 50) | 60 s | baseline |
| spike | 5-10x normal (e.g. 400) | 30 s | the jump |
| after | normal (e.g. 50) | 60 s | recovery |

Set `concurrency` on the spike run to at least `rate x p95 seconds x 2`. Give the "before" and "after"
runs the same assertions, and give the spike looser ones (or only `max_error_rate`) if the goal is
survival rather than speed. Run them back to back with no pause, the spike being the point. Use a small
wrapper on the server, started with `remote-run.sh --exec`:

```sh
#!/usr/bin/env bash
# spike.sh - run on the server from the Primus directory
set -u
dir=results/spike-$(date +%Y%m%d-%H%M%S)
for step in before spike after; do
  ./primus run configs/spike-$step.json --report-dir "$dir" --db primus.db >"$dir-$step.json" 2>>"$dir.log"
  echo "$step exit $?"
done
```

The verdict is the comparison: "after" p95 and error rate within ~10% of "before" means it recovered.
Errors during the spike are only acceptable if they are deliberate shedding (429/503 with
`Retry-After`). 5xx crashes or timeouts are findings. Report all three runs side by side with
`primus-report`.
