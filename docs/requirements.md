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
