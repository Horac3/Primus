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

## Current status: basic support

Today `auth_probe` is a mode value that runs a steady test (same engine
path as [load](load.md)) with global auth attached. There are **no**
dedicated attempt-count, request-rate, or per-credential fields yet —
attempt count is `total_requests`, pacing is "as fast as `concurrency`
workers can go":

```json
{
  "id": "cfg-probe-demo",
  "request_id": "login",
  "mode": "auth_probe",
  "concurrency": 2,
  "total_requests": 20,
  "assertions": {
    "expected_status": [200, 429]
  }
}
```

Set global auth first (see [global-auth](global-auth.md)) — without it the
probe fires unauthenticated, which usually just measures the login page's
401 rate. Listing both 200 and 429 as expected keeps rate-limited responses
from failing the run, so you can read the 200-vs-429 split in the status
counts instead.

Dedicated rate/credential fields and lockout-aware reporting are roadmap.
