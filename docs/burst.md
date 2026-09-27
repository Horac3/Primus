# Burst test

Pulsed traffic: requests are released in bursts of `count` with `idle`
milliseconds of silence between them. Use it to answer "does this endpoint
recover between pulses, or does latency ratchet up pulse after pulse?"
A steady [load](load.md) test can't show that — it never lets the target
breathe.

## Fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `id` | string | — | Config ID. |
| `request_id` | string | — | Endpoint to hit. |
| `mode` | `"burst"` | — | Selects this mode. |
| `concurrency` | int ≥ 1 | UI default 5 | Parallel workers. |
| `total_requests` | int > 0 | UI default 50 | Total requests across all bursts (the stop condition; `duration` must be unset). |
| `burst_pattern.count` | int ≥ 1 | UI default 10 | Requests per burst. |
| `burst_pattern.idle` | int ≥ 0 | UI default 500 | Milliseconds idle between bursts. |
| `assertions` | object | `{}` | See [assertions](assertions.md). |

With no `burst_pattern`, burst degrades to a steady load test.

## Example

```json
{
  "id": "cfg-burst-demo",
  "request_id": "findPetsByStatus",
  "mode": "burst",
  "concurrency": 5,
  "total_requests": 30,
  "burst_pattern": { "count": 10, "idle": 500 },
  "assertions": {
    "expected_status": [200],
    "max_error_rate": 0.1
  }
}
```

## Reading the result

Same cards as [load](load.md), plus the recovery question: compare p95
and error rate against the same endpoint's steady-load numbers. If the
first burst is clean but later bursts degrade, the target isn't recovering
during the idle gaps — try a longer `idle` to confirm, or treat it as a
capacity finding. The histogram shows whether slow responses cluster
(recovery failure) or spread evenly (uniform slowness).
