---
name: primus-auth
description: Set up authentication for Primus tests (bearer, API key, basic, OAuth or login-based credential pools for many users) and test auth behaviour itself - rate limiting and account lockout with auth-probe runs. Use whenever a tested API needs auth, when simulating many distinct users, or for auth, rate-limit, brute-force or lockout testing.
---

# Auth with Primus

Two jobs: giving test runs a valid identity, and testing the auth layer itself.

**Secrets never pass through Claude.** Tokens, passwords, client secrets and credential files are entered
by the person in Primus's web UI (through the SSH tunnel from `primus-remote`) or copied by them straight
to the server. Do not ask for them in chat, put them in config files, scripts, plans or commits, or echo
them in commands. Primus masks them after saving and keeps them out of logs, run JSON and reports.

## Giving runs an identity

| API uses | Primus setting | Where |
|---|---|---|
| A static token | Global auth `bearer` | Auth bar in the web UI |
| An API key header | Global auth `api_key` (header name + value) | Auth bar |
| HTTP Basic | Global auth `basic` | Auth bar |
| User logins, OAuth client credentials, many distinct users | A credential pool | Manage credential pools |

Global auth is one identity for every run, CLI runs included (it lives in `primus.db`). An endpoint whose
Authorization header has a literal value uses that value instead. An empty header, or `[GLOBAL]`, uses
global auth.

**Credential pools** make each worker log in as its own account before measurement starts. That matters
whenever per-user behaviour matters: per-user rate limits, caches, row-level locks, sessions. Steps to walk
the person through in the UI (details in Primus's `docs/credential-pools.md`):

1. Import credentials (CSV or JSON). The person uploads the file. It holds test accounts only, never real
   users.
2. Describe the login request with `{{field}}` placeholders, for example a JSON login
   `{"username": "{{username}}", "password": "{{password}}"}` or a form-encoded OAuth
   `grant_type=client_credentials&client_id={{client_id}}&client_secret={{client_secret}}`.
3. Say where the token is (JSON path like `data.access_token`, or a header) and how to send it
   (default `Authorization: Bearer {{token}}`).
4. **Test login**, then make it the pool runs log in as.
5. For runs longer than the token lifetime (soak), turn on **re-login on 401**.

The pool needs at least as many accounts as the run's `concurrency`, unless reuse is allowed. Primus can
create test accounts with **Provision accounts** (an explicit action, never during a run), which is
useful for a fresh staging tenant.

A config chooses its identity with `auth_source`: empty (the active pool, else global auth), `global`,
`pool`, or `none` (deliberately unauthenticated).

## Testing auth itself

### Rejections (status checks)

Add negative-case endpoints to the smoke collection with their own configs, each with `"auth_source": "none"`
or a literal bad header, and `expected_status` [401] or [403]: no token, a malformed token, and a valid
token for a role that must not reach an admin endpoint (a second pool, or a literal header the person
sets in the UI). Status checks are all Primus can assert today. Body checks are roadmap T1, and
cross-user access (BOLA) checks are T6.

### Rate limiting and lockout (`auth_probe`)

Repeated credentialed attempts against one endpoint, paced by `max_rate`. This looks exactly like a
brute-force attack: **only against systems the person owns or has written permission to test**, preferably
staging, and the person should warn whoever watches security alerts. Primus refuses it against
`PRIMUS_PROTECTED_HOSTS` without an explicit override.

```json
{
  "id": "cfg-probe-login",
  "request_id": "<login endpoint id>",
  "mode": "auth_probe",
  "concurrency": 2,
  "total_requests": 40,
  "max_rate": 2,
  "auth_source": "global",
  "assertions": { "expected_status": [200, 429] }
}
```

- **Where 429s start**: repeat with a rising `max_rate` (1, 2, 5, 10...) and read the 200-vs-429 split in
  `summary.status_counts`. Listing 429 as expected keeps throttling from failing the run.
- **Lockout**: use a dedicated test account. Probe its login with a wrong password in the body (expect
  401s), then send a few slow attempts with the right password. Still failing means the account is
  locked, not rate-limited. Ask how the account gets unlocked before starting.
- Report the rate where throttling begins, whether `Retry-After` is sent (check one response by hand), and
  the lockout threshold. Missing throttling on a login endpoint is a finding.
