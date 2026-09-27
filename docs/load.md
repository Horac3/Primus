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
