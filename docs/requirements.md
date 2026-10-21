# Requirements

Each requirement has a stable ID that tests, commits (`Refs:`) and the roadmap reference.
Format: `FR-<AREA>-<NN>` for functional, `NFR-<AREA>-<NN>` for non-functional. Never renumber;
retire an ID by marking it `(withdrawn)`.

Only planned work is written up here. Shipped behaviour is documented in the public pages under `docs/`
(`load.md`, `credential-pools.md`, `assertions.md`, …) and pinned by tests.

## Credential field masking (CRED)
- **FR-CRED-01** — A user can mark any credential field as masked or not, per field, in the pool panel.
  - Given a pool with a field `nickname`, when the user ticks "Mask this value", then every read of the pool
    except the engine's dispatch read returns that field masked.
  - Edge: unticking it returns the real value in the UI only after the user has saved the change.
- **FR-CRED-02** — On import, a field is masked by default when its name contains a secret word as a whole word.
  - Given fields `password`, `api_key`, `apiKey`, `client-secret`, `otp`, `PIN`, then all are masked.
  - Edge: `shipping`, `monkey`, `username`, `email`, `keyboard_layout` are not masked.
- **FR-CRED-03** — Pools created before this change keep every field masked that the old rule masked.
  - Given an existing pool with a field the old substring rule masked, when the migration runs, then it stays masked
    and the user can untick it.
- **FR-CRED-04** — Masked fields never appear in logs, saved run JSON, the result page, exports or the analyzer input.
  - Given a manually flagged field with an ordinary name (`nickname`) holding a known value, when a run completes,
    then that value is found in none of those places.

## Auth probe (PROBE)
- **FR-PROBE-01** — In auth-probe mode the UI labels the count field "Attempts"; it sets `total_requests`.
- **FR-PROBE-02** — An auth-probe run takes an optional `max_rate` (attempts per second) that caps how fast attempts
  are released. It is a ceiling, not a target: the reached rate is ≤ `max_rate` and ≤ concurrency/latency.
  - Given `max_rate` 20 and 6 attempts, when the run completes, then it took at least 250ms (5 intervals of 50ms).
  - Given a target that stalls both workers, when they free up, then consecutive attempts still go out at least
    1/`max_rate` apart (no catch-up burst).
  - Edge: 0 or empty means no cap. Negative, below 0.001, combined with a burst pattern, or set for any other mode,
    is rejected. Stop during a wait ends the run promptly.
- **FR-PROBE-03** — A run takes an optional `auth_source`: empty (an active pool, else global auth: unchanged),
  `global` (global auth even when a pool is active), `pool` (requires a pool) or `none` (no auth at all).
  - Given `auth_source` `none` and a header set to `[GLOBAL]`, when the run fires, then the target sees no auth header.
  - Edge: `pool` with no pool is rejected before the run starts. Web and CLI resolve it the same way.
- **FR-PROBE-04** — The rate and identity fields show only in auth-probe mode and are not submitted in other modes.
  The running detail line names the identity used.

## Open-loop rate mode (RATE) — draft, needs `/decide` and user approval
- **FR-RATE-01** — A user can run a test at a fixed request rate (requests/sec) for a fixed duration, independent of
  response time.
  - Given rate 500 and duration 15s, when the run starts, then requests are released on a schedule of 500/s whether
    or not earlier responses have returned.
- **FR-RATE-02** — A rate run has a maximum in-flight cap; requests that cannot be released because the cap is
  reached are counted as `Dropped`, never silently skipped.
  - Edge: cap reached for the whole run → `Dropped` is large and the verdict says the target could not sustain the rate.
- **FR-RATE-03** — The result reports target rate, achieved rate, and the latency measured from the intended send
  time (so slow servers do not hide queueing delay).
- **FR-RATE-04** — Stop cancels a rate run promptly; in-flight requests are `Cancelled`, not errors.

## Continuous integration (CI)
- **NFR-CI-01** — Every push and pull request runs build, vet, tests and `go test -race` before any image is published.
  - Given a failing race test, when the workflow runs, then the Docker publish job does not run.

## Run history query (HIST)
- **FR-HIST-01** — A natural-language question becomes a validated `Filter`; unknown fields, unknown enum values and
  out-of-range numbers are rejected. No SQL is generated.
- **FR-HIST-02** — Primus applies the filter over stored runs: modes, outcome (pass | fail, which covers every `fail_*` |
  no_assertions), stopped, time window (`within_hours`, or `since`/`until` dates), p50/p95/p99 bounds in ms, error-rate
  bounds, minimum total requests, target text (substring of the request name or URL, resolved locally), sort (newest |
  oldest | p95_desc | p95_asc | error_rate_desc) and limit (1–100, default 20).
  - Given "failed burst runs", then only burst runs with a `fail*` outcome are listed, newest first.
- **FR-HIST-03** — The result shows what the question was interpreted as and which interpreter ran (`ollama` or
  `rules`), and says so when Ollama was unavailable.
- **FR-HIST-04** — With Ollama absent, failing, slower than 20s or returning an invalid filter, the rules parser answers.
  - Edge: a question the parser cannot understand lists every run with a note saying it was not understood.
  - Edge: a blank question keeps the plain history list.
- **NFR-HIST-05** — Only the question, today's date and the filter schema go to the model: no run data, URLs, config or
  request names, bodies or credentials.
