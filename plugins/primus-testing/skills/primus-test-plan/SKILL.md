---
name: primus-test-plan
description: Plan API testing with Primus. Use when someone asks which tests to run against an API, wants a test plan, names a test type (smoke, functional, integration, regression, load, stress, security, UI, fuzz, burst, spike, soak, auth), or asks what Primus can and cannot test yet.
---

# Plan API tests with Primus

Primus is a self-hosted API testing tool (one Go binary, SQLite, web UI plus a headless CLI).
This skill turns "test my API" into a concrete plan of Primus runs, and routes each part to the
skill that executes it. Tests usually run on a **remote server**: use `primus-remote` for anything
that touches the machine.

## 1. Gather the inputs (ask only for what is missing)

- The API: an OpenAPI spec (file or URL) or a list of endpoints.
- The target environment and its base URL. Never assume production. Ask which hosts are production
  so they can go in `PRIMUS_PROTECTED_HOSTS` on the server.
- Auth: none, bearer token, API key, basic, or real user logins (credential pool).
- What "good" means: expected status codes, a p95 budget in ms, a max error rate, the traffic the
  API must carry (requests/sec or concurrent users).
- Where Primus runs: the server's SSH host, and whether Primus is already installed there.

## 2. Map each test type to Primus

| Test type | Primus today | Skill |
|---|---|---|
| Smoke | Count-based `load` run per endpoint, 1 worker, a few requests, `expected_status` assertions, run as a collection | `primus-smoke` |
| Load / capacity | `load` (closed loop, workers) or `rate` (open loop, fixed req/s) with p95 and error-rate assertions | `primus-load` |
| Stress | A ladder of `rate` runs that steps the rate up until assertions fail | `primus-stress` |
| Burst / spike | `burst` mode (pulses with idle gaps); a spike is a short high `rate` run between two baseline runs | `primus-spike-burst` |
| Soak | `soak` mode for a long duration, run in tmux on the server | `primus-soak` |
| Auth | Global auth, credential pools (many users), `auth_probe` for rate-limit and lockout behaviour | `primus-auth` |
| Regression | The same collection run before and after a change, compared run by run against a saved baseline | `primus-regression` |
| Reporting | Reading run JSON, Markdown reports and series CSV, comparing runs | `primus-report` |

### Not supported yet (say so plainly; do not fake it)

Primus checks status codes, p95 and error rate only. These are on the Primus roadmap
(`docs/progress.md`, steps T1-T6) and are not possible yet:

- **Functional** (response body and header checks against requirements): roadmap T1. Until then the
  closest Primus check is status codes, including negative cases (a request that must return 404,
  401 or 422 gets its own endpoint with that `expected_status`). Say that this covers status only.
- **Integration** (multi-step flows passing values between calls): roadmap T2. Primus runs one
  endpoint per test config; it cannot carry an ID from a create call into a read call.
- **Security** beyond auth-probe (BOLA/BFLA, headers, injection): roadmap T6. `auth_probe` covers
  rate limiting and lockout today.
- **Fuzz** (mutated and malformed input): roadmap T5. A `payload_pool` of hand-written invalid bodies
  is the nearest thing; label it as a fixed negative-input set, not fuzzing.
- **UI testing**: out of scope. Primus is API-only and has no browser.

If the person needs one of these now, offer to write the Primus requirement for that roadmap step
instead of working around it with another tool.

## 3. Write the plan

Produce a short table: test type, endpoints, Primus mode and settings, assertions, where and when it
runs, and which skill runs it. Order it smoke first (cheap, catches broken deploys), then load, then
stress, spike/burst and soak, then regression in CI. Mark anything that targets production as a
smoke check only, and confirm it with the person.

## Safety rules that apply to every plan

- Heavy tests (load, stress, spike, burst, soak, auth-probe) go to a non-production environment.
  Put production hostnames in `PRIMUS_PROTECTED_HOSTS` on the server; Primus then refuses heavy runs
  against them unless a config names the host in `allow_protected_host`. Only set that override when
  the person explicitly says they own the system and want to load production, and repeat back which
  host it is for.
- Auth-probe sends traffic that looks like a brute-force attack. Only run it against systems the
  person owns or has written permission to test, and tell them to warn whoever watches the alerts.
- Never put tokens, passwords or API keys in config files, plans, chat or commit messages. They go
  into Primus's global auth or credential pools on the server (see `primus-auth`).
