# Credential pools

A credential pool lets one run act as many distinct, authenticated users
instead of one shared identity. Before measurement starts, each worker logs
in once as its own account, keeps the token it gets back, and sends that
token on every request it dispatches.

**The accounts must already exist on the target.** Primus never signs up or
creates accounts. Import credentials for users you have already provisioned
(a seeded test database, a fixture script, an identity provider's test
tenant).

## How a pooled run works

1. **Login phase.** Primus logs in the credentials the run needs, at most
   *login concurrency* at a time (default 20). Worker `i` uses credential
   `i % pool size`.
2. **Measurement.** Dispatch starts only after every login has finished.
   Login time never counts toward request latency, requests/sec or a soak
   run's duration.
3. **Report.** The result shows a **Logins** section: attempted, succeeded,
   failed, login p50/p95, and the top failure reasons (`HTTP 401`,
   `timeout`, `token not found: ...`). Logins interrupted by Stop are shown
   as `interrupted by stop` (`cancelled` in JSON), not as failures.

Failure rules:

- A worker whose login failed is left out of the run; the others carry on.
- If no login succeeds, the run fails at once with **FAILED — no successful
  logins** and sends no requests. A pooled run never falls back to
  sending requests unauthenticated.
- A pool smaller than the run's concurrency is rejected, unless you tick
  **Allow credential reuse**. With reuse, workers that share an account use
  one login and its token.

## Set one up

Open **Manage credential pools** in the auth bar.

### 1. Import credentials

CSV, with a header row naming the fields:

```csv
username,password
alice,s3cret-a
bob,s3cret-b
```

Or JSON, as an array of objects:

```json
[
  {"email": "alice@example.com", "password": "s3cret-a", "tenant": "acme"},
  {"email": "bob@example.com",   "password": "s3cret-b", "tenant": "acme"}
]
```

Field names are up to you (`username`, `email`, `client_id`,
`client_secret`, ...). A pool holds up to 10,000 rows. The import reports
each bad row by row number and skips it. Duplicate usernames or emails
produce a warning, but the rows are kept.

### 2. Describe the login request

The login step is one request template: method, URL, headers and body.
`{{field}}` placeholders are filled from each credential row. A
placeholder that doesn't match any field is rejected when you save.

JSON login:

```text
POST https://api.example.com/auth/login
Content-Type: application/json

{"username": "{{username}}", "password": "{{password}}"}
```

Form-encoded login, for example an OAuth client-credentials grant:

```text
POST https://auth.example.com/oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials&client_id={{client_id}}&client_secret={{client_secret}}
```

Values are escaped to suit the body's `Content-Type`: JSON string
escaping for JSON, URL encoding for form bodies. For HTTP Basic logins, add
a column holding the pre-encoded `base64(user:password)` value and send it
with an `Authorization: Basic {{basic}}` header.

### 3. Extract the token

| Your login response | From | Path | Strip prefix |
|---|---|---|---|
| `{"data": {"access_token": "eyJ..."}}` | JSON body | `data.access_token` | — |
| `{"token": "abc"}` | JSON body | `token` | — |
| `{"tokens": [{"value": "abc"}]}` | JSON body | `tokens.0.value` | — |
| header `X-Auth-Token: abc` | Header | `X-Auth-Token` | — |
| header `Authorization: Bearer abc` | Header | `Authorization` | `Bearer ` |

The value must be a non-empty string. Otherwise that login fails with
`token not found`.

### 4. Choose how the token is sent

The default is `Authorization: Bearer {{token}}`. Change the header and
format to fit your API, for example header `X-Auth-Token` with format
`{{token}}`.

### 5. Test, then activate

**Test login** logs in one credential and shows the HTTP status, latency,
and the extracted token with all but its last 4 characters masked. When
it works, pick the pool under **Runs log in as** (or click **Use this pool
for runs**). Runs now use the pool until you switch back to
**Global auth**.

## Precedence

- An active pool replaces [global auth](global-auth.md) for every run.
  Probes (**Test connection**) still use global auth.
- Per-endpoint headers behave as before. A header set to `[GLOBAL]` (or
  left empty) receives the worker's pool token. An explicit value
  overrides the pool token for that endpoint.

## Re-login on 401

Off by default. When on, a worker that gets a `401` logs in again once and
retries with the fresh token on its next request. Workers that share an
account (reuse) coordinate: one of them logs in again and the others adopt
its token, so single-session APIs don't revoke each other's tokens.
Re-logins are counted separately from the login phase. A re-login happens
during measurement, so its time does count toward the run's wall-clock
time.

## Secrets

- Masking is set per field. Under the preview table, **Masked fields** has a
  "Mask this value" box for each field; tick or untick it and press
  **Save masked fields**. A masked value shows as `••••••` everywhere except
  the login request itself, and an unmasked one shows only after you save.
- On import, a field is masked by default when its name contains a secret
  word as a whole word: `password`, `passwd`, `pwd`, `pass`, `passphrase`,
  `secret`, `token`, `key`, `apikey`, `pin` or `otp`, or is one of
  `accesstoken`, `authtoken`, `clientsecret`, `newpassword`, `passcode`
  or `pincode`. Words are split on `_`, `-`, spaces, digits and camelCase,
  so `api_key`, `apiKey`, `client-secret`, `password2` and `PIN` are
  masked, while `shipping`, `monkey` and `keyboard_layout` are not. Mask
  any other field (say `nickname`) by ticking it.
- Pools created before per-field masking keep every field the old rule
  masked (any name containing `pass`, `secret`, `token`, `key`, `pin` or
  `otp`). Untick the ones that are not secret.
- Masked fields, passwords and tokens are never written to the server log
  or the run record, and never printed by `primus run`. Login failures are
  reported as short classes, never as raw responses.
- The AI analyzer only receives login counts (attempted, succeeded,
  failed). It never sees pool names, credentials or tokens.
- Credentials are stored in plain text in the SQLite file, the same as
  global auth. Protect the `/data` volume.

## CLI

Set `credential_pool_id` in the `TestConfig` JSON to run with a pool from
the same database. The JSON output includes the `login` report. See
[CLI](cli.md).
