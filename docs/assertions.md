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

Transport errors (connection refused, connection reset, timeouts) always
count as errors. Unexpected status codes count as errors too, but only when
`expected_status` is configured.

## Cancelled requests

Requests still in flight when the run ends are **cancelled**: they are
neither a success nor an error. That happens when you press Stop, and when a
Duration run reaches its deadline with requests still waiting on the target.
A cancelled request is left out of the request total, the error count and
error rate, the latency percentiles and the status-code counts, so a stop can
never push a run over its max error rate. It is counted once, in
`cancelled`, and the result card says how many were dropped:
`12 in-flight requests abandoned on stop`, or `cut off at the end of the run`
for a Duration deadline. The note only appears when the count is above 0.

What decides it is whether the *run* had ended, not what the error looks
like. A request that times out on its own while the run is still going is a
target timeout and counts as an error. The flip side: a genuine connection
error that lands in the same instant as Stop is counted as cancelled. Every request Primus sends is counted
exactly once: `total_requests + cancelled` is everything dispatched.

Logins work the same way in a credential-pool run: a login interrupted by Stop
is counted as cancelled (`interrupted by stop`), not as a failed login.

## The tri-state verdict (and why it's three states, not two)

| State | Banner | Meaning |
|---|---|---|
| PASS | green `✓ PASSED` | Every configured assertion held. |
| FAILED | red `✗ FAILED` | At least one assertion failed — or no request completed (`✗ FAILED — no requests completed`), which fails unconditionally. A [fixed-rate](load.md) run that dropped any request also fails unconditionally (`✗ FAILED — target rate not sustained`). |
| No assertions | neutral `◎ Completed — no assertions configured` | Requests went through, but nothing was asserted. |

"No requests completed" means no HTTP response came back at all: every
request was a transport error, or the run was stopped before the first
response. It fails even when the only assertion is a max error rate —
0 errors out of 0 requests is not a pass. A run stopped with the Stop
button keeps the verdict its completed requests earned, and the result
card marks it `⏹ Stopped early`.

The third state exists because of a real bug this rule prevents: a run
with zero assertions used to render a green PASS, which reads as "this
endpoint is fine" when nothing was actually checked. Neutral is honest —
green is reserved for thresholds actually met. `Passed` is false for
anything but a real pass, so CI gating on the exit code / `passed` field
can never mistake "no assertions" for success.
