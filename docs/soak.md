# Soak test

Duration-only steady traffic: like [load](load.md), but the stop condition
is always a deadline, never a count. Use it to answer "does this endpoint
slowly degrade — leaking memory, connections, or rate-limit budget — over
minutes?" A short load test finds the immediate breaking point; soak finds
the slow one.

## Fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `id` | string | — | Config ID. |
| `request_id` | string | — | Endpoint to hit. |
| `mode` | `"soak"` | — | Selects this mode. |
| `concurrency` | int ≥ 1 | UI default 5 | Parallel workers. |
| `duration` | int > 0, **nanoseconds** | UI default 30 s | How long to run. 5 minutes = `300000000000`. `total_requests` must be unset. |
| `assertions` | object | `{}` | See [assertions](assertions.md). |

## Example — 2 minutes at concurrency 3

```json
{
  "id": "cfg-soak-demo",
  "request_id": "findPetsByStatus",
  "mode": "soak",
  "concurrency": 3,
  "duration": 120000000000,
  "assertions": {
    "expected_status": [200],
    "max_error_rate": 0.05
  }
}
```

## Reading the result

Same cards as [load](load.md). For degradation, the number that matters is
p95 and error rate *relative to a short baseline run*: flat across the soak
means healthy; climbing means something is accumulating server-side.

## Honest limits (demo-scale)

This is seconds-to-minutes soaking, not true multi-hour soak testing:

- No trend graphing across runs — compare two runs' cards by hand.
- No mid-run metrics; you get one aggregate at the deadline.
- The web UI runs it asynchronously with a Stop button, so a long soak
  won't hang your browser — but it does hold server resources until it
  finishes or you stop it.

True multi-hour soak with trend charts is roadmap, not this.
