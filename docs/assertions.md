# Assertions and verdicts

Assertions turn metrics into a verdict. Each configured assertion gets one
checklist row (`Status code: 200`, `Latency (p95) < 500ms`,
`Error rate < 5%`); the overall verdict follows the tri-state rule below.

## Fields

| Field | Type | Meaning |
|---|---|---|
| `expected_status` | int[] | Every recorded status code must be in this list. Anything else fails the run *and* counts toward the error rate. Omit for "any HTTP response counts as a response". |
| `max_p95_millis` | int > 0 | p95 latency must stay under this many ms. `0`/omit = off. |
| `max_error_rate` | float 0–1 | Error rate must stay under this fraction (`0.05` = 5%). `0`/omit = off. |

Transport errors (connection refused, timeouts) always count as errors.
Unexpected status codes count as errors too, but only when
`expected_status` is configured.

## The tri-state verdict (and why it's three states, not two)

| State | Banner | Meaning |
|---|---|---|
| PASS | green `✓ PASSED` | Every configured assertion held. |
| FAILED | red `✗ FAILED` | At least one assertion failed — or 100% of requests failed, which fails unconditionally. |
| No assertions | neutral `◎ Completed — no assertions configured` | Requests went through, but nothing was asserted. |

The third state exists because of a real bug this rule prevents: a run
with zero assertions used to render a green PASS, which reads as "this
endpoint is fine" when nothing was actually checked. Neutral is honest —
green is reserved for thresholds actually met. `Passed` is false for
anything but a real pass, so CI gating on the exit code / `passed` field
can never mistake "no assertions" for success.
