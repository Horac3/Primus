# Credential pools

A credential pool lets one run act as many distinct, authenticated users
instead of one shared identity. Before measurement starts, each worker logs
in once as its own account, keeps the token it gets back, and sends that
token on every request it dispatches.

**The accounts must exist on the target before a run starts.** Either import
credentials for users you have already provisioned (a seeded test database, a
fixture script, an identity provider's test tenant), or create new ones with
[Provision accounts](#provision-accounts). Primus never signs up accounts
during a run.

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

Turn it on when the target's tokens can expire before the run ends,
which is typical for a soak run against short-lived access tokens. With
it off, every request after expiry gets a `401`. Those count as errors,
and fail the run, only when [`expected_status`](assertions.md) excludes
401. Without it they are counted as completed requests and the run can
still pass. Leave it off for short load and burst runs, where tokens
outlive the run and a `401` usually means a wrong credential or a broken
login extraction. Re-login won't hide those.

It runs at most once per worker per run, so it covers one expiry. If the
run lasts more than about twice the token lifetime, the fresh token
expires too and later requests `401` again. Shorten the run or ask the
target for longer-lived tokens.

## Provision accounts

Open **Provision accounts** in the pool panel to sign up new accounts on the
target before you test. It is a separate action that you start; a run never
signs anyone up.

1. Choose **New pool…** (and name it) or an existing pool to add to.
2. Set **Accounts** (how many) and **Concurrency** (sign-ups in flight at once).
   An existing pool can grow up to the 10,000-account limit.
3. List the **account fields**, one `name=template` per line. Two values are
   generated for each account: `{{seq}}` counts 1, 2, 3… (continuing after the
   pool's existing accounts) and `{{secret}}` is a random 24-character password
   unique to that account.
   ```
   email=user{{seq}}@example.test
   password={{secret}}
   ```
4. Describe the **sign-up request** (method, URL, headers, body). Use the field
   names as `{{field}}` placeholders, for example
   `{"email":"{{email}}","password":"{{password}}"}`.
5. Optionally **keep a value from the response**, such as the new user's id
   (`user_id` from `id`), as an extra field on the row.

Primus sends one request per account. A response with an HTTP 2xx status adds a
row to the pool; anything else adds nothing. The panel shows how many accounts
were created, failed or were cancelled, with the failure reasons (`HTTP 409`,
`timeout`, `connection error`). Stop keeps the accounts already created and
counts the rest as cancelled. If a 2xx response lacks the value you asked to
keep, the account is still saved and the summary says so.

Fields built from `{{secret}}` are masked, like any other secret field. A new
pool starts with a default login step: set its login URL (and the token path)
under **Login step** before using it for runs.

Limits you should know:

- **Accounts stay on the target.** Primus cannot delete them, so use a test
  system and expect the data to remain after the run.
- No email verification and no captcha handling: the target must accept a plain
  sign-up request.
- A sign-up in flight when you press Stop may already exist on the target even
  though Primus never saw the response, so that account is not saved. The next
  batch continues the `{{seq}}` count after the last number attempted, so it
  will not reuse those names.
- **Concurrency** cannot exceed the pool's login concurrency (20 for a new
  pool), and only one provisioning job can run per pool at a time.
- Sign-up endpoints are often rate limited. Lower the concurrency if you see
  `HTTP 429`.

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
