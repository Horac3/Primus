# Auth-probe test

> **Safety first: only probe targets you are authorized to test.**
> Repeated credentialed requests look exactly like a credential-stuffing or
> brute-force attack to everyone else. Get written permission for third-party
> targets; on your own systems, prefer staging and tell whoever watches the
> alerts.

Repeated credentialed requests against one endpoint, to observe
rate-limit and lockout behavior: at what request rate do `429`s start?
Does the account lock after N bad passwords, and does it unlock?

- **Rate-limit** = the *target* throttles you (usually HTTP 429, often
  with a `Retry-After` header). Retrying slower succeeds.
- **Lockout** = the *account or credential* is disabled after too many
  failures. Retrying — slowly or not — keeps failing until something
  resets it. Don't confuse the two when reading results.

## Fields

Pick **Auth probe** as the mode. The panel then shows:

- **Attempts** — how many requests to fire (`total_requests`).
- **Max attempts per second** (`max_rate`, optional) — a ceiling on how
  fast attempts are released: each attempt goes out at least
  1 ÷ `max_rate` seconds after the previous one. After a slow response
  Primus does not "catch up" with a burst, so a rate limiter never sees
  more than `max_rate`. It is not a target. Each attempt also waits for a
  free worker, so the rate you reach is at most `max_rate` *and* at most
  concurrency ÷ latency. Leave it empty for no cap (as fast as
  `concurrency` workers can go). The smallest cap is 0.001 (one attempt
  per ~17 minutes). Only auth-probe accepts it, and not together with a
  `burst_pattern`; open-loop fixed-rate load is a separate, planned mode.
- **Send as** (`auth_source`) — which identity the attempts carry:

| Value | Identity |
|---|---|
| *(empty)* | The active [credential pool](credential-pools.md) if one is set, otherwise [global auth](global-auth.md). |
| `global` | Global auth, even when a pool is active. |
| `pool` | The active credential pool. The run is rejected if no pool is active. |
| `none` | No auth at all. A header set to `[GLOBAL]` is left out. |

The running line names the identity ("global auth", "no auth", or the
pool's name). Stop ends the run promptly, even mid-wait.

```json
{
  "id": "cfg-probe-demo",
  "request_id": "login",
  "mode": "auth_probe",
  "concurrency": 2,
  "total_requests": 20,
  "max_rate": 2,
  "auth_source": "global",
  "assertions": {
    "expected_status": [200, 429]
  }
}
```

This sends 20 attempts with global auth, no faster than 2 per second
(about 10 seconds). Listing both 200 and 429 as expected keeps
rate-limited responses from failing the run, so you can read the
200-vs-429 split in the status counts instead. Without `expected_status`,
a 401 counts as a completed request, not an error.

To find the rate where `429`s start, repeat the probe with a rising
`max_rate`. To check lockout, point the probe at a test account's login
endpoint with a wrong password in the request body, then probe again
slowly with the right one: if it still fails, the account is locked,
not rate-limited.

Lockout-aware reporting (for example "first 429 at attempt N") is on
the roadmap.
