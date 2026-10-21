# Load test

Steady traffic: `concurrency` workers fire requests as fast as they can
until a stop condition holds. Use it to answer "does this endpoint hold up
under sustained parallel traffic, and how slow does it get?"

## Stop condition (exactly one, required)

| Field | Type | Meaning |
|---|---|---|
| `total_requests` | int > 0 | Stop after this many requests. |
| `duration` | int > 0, **nanoseconds** | Stop after this long. 60 seconds = `60000000000`. |

Set exactly one — the run is rejected otherwise. In the web UI, load is
duration-driven (default 30 s); `total_requests` configs still work via the
CLI and JSON.

## Other fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `id` | string | — | Config ID (referenced by stored runs). |
| `request_id` | string | — | Endpoint to hit (must exist in the DB). |
| `mode` | `"load"` | — | Selects this mode. |
| `concurrency` | int ≥ 1 | UI default 5 | Parallel workers. |
| `assertions` | object | `{}` (none) | See [assertions](assertions.md). |

## Example

```json
{
  "id": "cfg-load-demo",
  "request_id": "findPetsByStatus",
  "mode": "load",
  "concurrency": 5,
  "total_requests": 20,
  "assertions": {
    "expected_status": [200],
    "max_p95_millis": 3000,
    "max_error_rate": 0.1
  }
}
```

Duration-based equivalent (60 s): replace `"total_requests": 20` with
`"duration": 60000000000`.

## Reading the result

- **Verdict banner** — PASS / FAILED / neutral, see [assertions](assertions.md).
- **Metric cards** — p50/p95/p99 latency in ms, requests/sec
  (`total_requests` ÷ wall time), error count and rate, per-status counts.
- **Histogram** — response-time distribution over fixed buckets
  (0–49, 50–99, 100–149, 150–199, 200–299, 300–399, 400–499, 500–599,
  ≥600 ms). Bars scale to the tallest bucket. Only completed responses
  are bucketed; transport errors have no latency sample.
- **Checklist** — one row per configured assertion (✓ PASSED / ✗ FAILED).

## Fixed-rate mode (`"mode": "rate"`)

Plain load is closed-loop: each worker waits for its response before
sending the next request, so throughput is concurrency ÷ latency. A server
that slows down therefore receives *less* traffic, and the latency you see
leaves out the time real users would have spent waiting. Fixed-rate mode is
open-loop: requests go out on a schedule, whether or not earlier ones have
answered.

| Field | Type | Default | Meaning |
|---|---|---|---|
| `mode` | `"rate"` | — | Selects fixed-rate mode. |
| `target_rate` | number ≥ 0.01 | UI default 50 | Requests per second to release. Required in this mode, rejected in every other. |
| `duration` | int > 0, **nanoseconds** | UI default 30 s | Required; `total_requests` is not accepted here. |
| `concurrency` | int ≥ 1 | UI default 5 | **The most requests in flight at once**, not a rate. With a [credential pool](credential-pools.md), accounts whose login failed lower this cap. |

How it runs:

- Request *n* is due at `start + n / target_rate`. The schedule is fixed at
  the start, so it never drifts.
- A request that is due while `concurrency` requests are already in flight
  is **dropped**: never sent, counted in `dropped`, never skipped silently.
  Raise `concurrency` to at least `target_rate × expected latency (s)` so a
  healthy server has room.
- Latency is measured from each request's **scheduled** send time, so any
  delay in sending it counts as latency instead of being hidden.
- `burst_pattern` and `max_rate` cannot be combined with it.
- Stop works as in the other modes: requests still in flight are
  `cancelled`, not errors.

The result adds **Target rate**, **Achieved rate** (requests released per
second of the run's duration, or of the time until Stop) and **Dropped**. Any drop fails the run with
`✗ FAILED — target rate not sustained` (outcome `fail_rate_not_sustained`),
even when no assertions are configured: the server could not take the
requested rate within the in-flight cap.

```json
{
  "id": "cfg-rate-demo",
  "request_id": "findPetsByStatus",
  "mode": "rate",
  "target_rate": 500,
  "concurrency": 100,
  "duration": 15000000000,
  "assertions": { "max_p95_millis": 300 }
}
```
