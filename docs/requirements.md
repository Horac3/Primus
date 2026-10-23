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

## Commit-message assistant (META) — developer tooling, not a product feature
Lives under `tools/`. Local Ollama only; never a hosted model.
- **FR-META-01** — Generate from the staged diff: the tool reads `git diff --cached` and prints a draft commit message
  (subject, body bullets, `Tests:` line) for the maintainer to review. It never runs `git commit` or changes the index.
  - Given staged changes under `internal/engine`, then the draft's scope is `engine`.
  - Edge: an empty staged diff exits non-zero with a message and calls no model.
  - Edge: a diff larger than the model input cap is reduced to file names, stat counts and truncated hunks, and the
    output says it was truncated.
- **FR-META-02** — Format validation: a draft is accepted only if it matches the project's Conventional Commits format
  from `CLAUDE.md`: `type(scope): subject` with a scope from the allowed list (`engine`, `model`, `store`, `web`, `ui`,
  `insight`, `author`, `auth`, `importer`, `cli`, `docs`, `docker`, `ci`, `tools`), bulleted body, final `Tests:` line. An invalid
  draft is never printed as if valid.
- **FR-META-03** — Fallback: with Ollama absent, failing, slower than a time cap or returning an invalid draft, a
  rules-based draft is built from the staged paths (scope from the path, type from the change kind) and the output says
  which generator ran (`ollama` or `rules`).
- **FR-META-04** — Excluded from the binary and image: nothing under `tools/` is imported by `cmd/primus` or
  `internal/...`, and `deploy/Dockerfile` does not copy it into the final image.
  - Given `go list -deps ./cmd/primus`, then no `tools/` package appears.

## Downloadable run reports (REPORT)
- **FR-REPORT-01** — `GET /runs/{id}/report.html` downloads (`Content-Disposition: attachment`) a self-contained HTML
  report: inline CSS, no scripts, no `hx-*`, no external URLs. It shows the verdict (tri-state wording), mode,
  concurrency, start, finish and duration, the endpoint, summary metrics (total, errors, error rate, p50/p95/p99,
  cancelled, dropped, and target/achieved rate in `mode: rate`), assertions, the time-series SVG charts, the latency
  histogram, status counts, error causes, the login summary when a pool ran, and the insight with the analyzer and
  model that produced it (or "no insight generated").
- **FR-REPORT-02** — `GET /runs/{id}/series.csv` has the header `elapsed_ms,avg_latency_ms,rps,error_rate` and one row
  per time-series point; a run without a series gets the header only.
- **FR-REPORT-03** — `GET /runs/{id}/report.md` is a Markdown summary with the same facts as the HTML report, without
  charts. Metrics, assertions and errors are tables; the insight sits under a heading that names the analyzer.
- **FR-REPORT-04** — No export, run page or result card contains a URL query string, URL userinfo, header, body,
  credential or token. URLs inside error causes keep only `scheme://host/path`. The endpoint is shown as
  `METHOD scheme://host/path`, or "endpoint deleted" when the request or config no longer exists.
  - Given an error cause `Get "http://u:p@h/x?api_key=s": dial tcp ...`, then no export contains `api_key` or `u:p`.
- **FR-REPORT-05** — An unknown run ID returns 404 on all three routes. The run page links to the three downloads.

## Credential provisioning (PROV)
Decision: D-016. Provisioning is a user-started pool action, never part of a run.
- **FR-PROV-01** — A user can start provisioning on a new or existing pool by giving a sign-up request template (like
  the login spec), a count N, and an optional `Extraction` for fields to keep from the response (e.g. `user_id`).
  The template accepts the placeholders `{{seq}}` (1..N) and `{{secret}}` (random per account, from `crypto/rand`).
  - Given N=5 and a body `{"email":"u{{seq}}@x.test","password":"{{secret}}"}`, then five requests are sent with
    distinct emails and distinct passwords.
  - Edge: N above `MaxCredentialPoolRows` (counting rows already in the pool) is rejected before any request is sent.
- **FR-PROV-02** — A row is stored only for a sign-up that returns 2xx. The row holds the generated username/secret
  fields and any extracted fields; the secret is masked like any masked pool field (FR-CRED-04).
  - Edge: a 4xx/5xx or transport error adds no row and counts as failed.
- **FR-PROV-03** — Provisioning runs asynchronously through the run registry pattern (`context.WithoutCancel`), with
  concurrency capped at the login concurrency. Stop cancels it; rows already stored stay, and requests cancelled by
  Stop count as cancelled, not failed.
- **FR-PROV-04** — The result reports attempted, succeeded, failed and cancelled counts with failure reasons (status
  code or cause), like `LoginSummary`, polled every second in the UI.
  - Given a target that returns 409 for a duplicate, then the summary lists "409" with its count.
- **FR-PROV-05** — No generated secret, token or sign-up request body appears in logs, error messages, the summary,
  exports or analyzer input. URLs in failure causes are reduced to `scheme://host/path` (FR-REPORT-04).
- **FR-PROV-06** — Runs never call provisioning. A run with a pool behaves exactly as before (login phase, then
  requests); provisioned rows are ordinary pool rows.
- **FR-PROV-07** — `docs/credential-pools.md` documents provisioning, its limits (no account deletion on the target,
  no email verification, no captcha) and that accounts stay on the target; it no longer says Primus never signs up.

## Collections (COLL)
Decision: D-017.
- **FR-COLL-01** — A user sees endpoints grouped by collection in the sidebar; each collection can be expanded or collapsed.
  - Given two collections, when the page loads, then each lists its own endpoints and the expanded state survives a reload.
  - Edge: an empty collection shows "no endpoints yet" and an add-endpoint control.
- **FR-COLL-02** — On upgrade, existing endpoints move into a collection named "My endpoints".
  - Given a database from before collections, when the app starts, then every endpoint, run and insight is still reachable.
  - Edge: running the migration twice changes nothing.
- **FR-COLL-03** — A user can create, rename and set the base URL of a collection; new endpoints are created inside a chosen collection.
  - Edge: a duplicate or empty name is rejected with a message.
- **FR-COLL-04** — A user can delete a collection.
  - Given a collection with 8 endpoints and 12 runs, when the user confirms the prompt that states "8 endpoints, 12 runs", then all of them are gone.
  - Edge: refused with a clear message if any run in it is active; nothing is deleted.
- **FR-COLL-05** — A user can clear history for one endpoint, one collection, or everything.
  - Given runs exist, when the user confirms (prompt shows the count), then runs and insights are removed and endpoints and configs remain.
  - Edge: refused while a run in scope is active. Clearing when there is no history says so and does nothing.
- **FR-COLL-06** — Built-in collections can be deleted and restored.
  - Given the built-in collection was deleted, when the app restarts, then it does not reappear; when the user picks "Restore examples", it does.
  - Edge: restoring when it already exists reports "already present" and changes nothing.
- **FR-COLL-07** — A user can run every endpoint in a collection, one after another, using each endpoint's saved test config, and stop the whole sequence.
  - Edge: an endpoint without a saved config is skipped and listed as skipped. Stop cancels the current run and the rest.
- **FR-COLL-08** — Importing an OpenAPI spec creates a collection named from the spec's title and puts the imported endpoints in it.
  - Edge: a title that already exists gets a numeric suffix.

## Seed data (SEED)
- **FR-SEED-01** — A fresh install contains a built-in collection "JSONPlaceholder" with about 20 endpoints covering
  `/posts`, `/comments`, `/albums`, `/photos`, `/todos` and `/users`: list, get by id, a 404 case (`/posts/9999`),
  nested routes (`/posts/1/comments`, `/users/1/posts`), query filters (`/comments?postId=1`, `/todos?completed=false`),
  and POST, PUT, PATCH, DELETE on `/posts`.
- **FR-SEED-02** — Every seeded endpoint has a modest saved test config (for example 20 requests, concurrency 2) and
  assertions that match the real behaviour: 200 for reads, 201 for create, 404 for the missing id, and a loose p95 limit.
  - Edge: the large `/photos` list gets a higher latency limit and a note about payload size.
- **FR-SEED-03** — Each seeded endpoint carries a short note a reader can learn from, and the writes say the server
  fakes them (create returns 201 but the record is not stored, so a following GET returns 404).
- **FR-SEED-04** — Seeding is idempotent and never overwrites user edits; the collection's `base_url` is
  `https://jsonplaceholder.typicode.com` and can be changed.
- **FR-SEED-05** — A public docs page `docs/collections.md` explains the built-in collection, its fake writes, that it
  is a third-party free service run by someone else (keep load low, only test what you are allowed to), and links to
  jsonplaceholder.typicode.com. It is added to the embed list and to `order` in `docs/docs.go`.

## Per-endpoint reports (REP)
- **FR-REP-01** — Each endpoint has a report page showing: method, URL, collection, the latest run's summary, a table of
  the last 10 runs (time, requests, p50, p95, p99, error %, throughput, cancelled, verdict), and findings.
  - Edge: no runs yet shows "no runs yet" and a run button.
- **FR-REP-02** — Every report includes a fixed "how to read this" key defining: p50, p95, p99, error rate, throughput,
  concurrency versus requests per second, cancelled requests, login latency (when a pool is active), and the three verdicts.
  - Edge: the key is the same text for every endpoint and is not produced by the model.
- **FR-REP-03** — A collection page lists its endpoints in a table: last verdict, p95, error %, last run time, with a link to each report.
  - Edge: an endpoint never run shows `-` in every column.

## Richer insights (INS)
- **FR-INS-01** — Per-endpoint insights have fixed sections: "What the numbers say", "Likely causes", "What to try next".
  The rules analyzer and Ollama produce the same sections, and the UI says which analyzer wrote them.
- **FR-INS-02** — Ollama sections are grounded: any number they quote must match the supplied aggregates (tolerant
  comparison); otherwise the endpoint falls back to the rules analyzer for that report.
  - Edge: only aggregates and normalized URLs are sent; never bodies, headers, credentials or tokens.
- **FR-INS-03** — Insights may compare against the endpoint's previous runs, using aggregates only (for example "p95 rose from 120 ms to 310 ms").
  - Edge: with one run there is no comparison section.
