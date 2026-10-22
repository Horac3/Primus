# Decisions

Append-only log. Newest at the bottom. `/decide` drafts entries in this shape; supersede an entry by
adding a new one that references it, never by editing history.

## D-001: Use claude-kit workflow
Date: 2026-10-21
Status: accepted
Context: Sessions need consistent rules, cheap context, and clean handoffs between conversations and models.
Decision: Follow the claude-kit loop: `/next` per roadmap step, test-first, `opusplan` with delegated
subagents, state in `docs/progress.md`. Replaces the earlier hand-written `.bob/rules` and `AGENTS.md` guidance.
Consequences: Every conversation starts with `/next` and ends with `/handoff`. `AGENTS.md` stays as a leftover.
Revisit if: the overhead outweighs the benefit for this project.

## D-002: htmx and server-rendered templates, not React
Date: 2026-10-21
Status: accepted (imported)
Context: Primus ships as one binary; the UI is forms, fragments and small SVG charts.
Decision: `html/template` plus an embedded htmx. No JS framework, bundler or build step.
Consequences: Cheaper to ship and host than a React frontend; interactions are limited to what swapped fragments can do.
Revisit if: a feature needs rich client state that fragments cannot express.

## D-003: One dispatch loop; closed-loop load
Date: 2026-10-21
Status: accepted (imported)
Context: Load, burst and soak differ only in pacing and stop condition.
Decision: `engine.Run*` serves all three. Stop is `TotalRequests` XOR `Duration`. Runs are async via an in-memory
registry with 1s polling; the registry uses `context.WithoutCancel` on the request context.
Consequences: Throughput is concurrency / latency, so Primus cannot yet hold a fixed request rate (coordinated omission).
Revisit if: the open-loop rate mode (roadmap step 7) lands.

## D-004: Global auth singleton plus credential pools through one `applyAuth` path
Date: 2026-10-21
Status: accepted (imported); "account creation is out of scope" superseded by D-016
Context: Users need one place to set auth, and also N distinct logged-in users for concurrency tests.
Decision: `global_auth` is a singleton row; `[GLOBAL]` header sentinel opts an endpoint in. An active pool overrides
it for a run, resolving one token per worker before measurement. Accounts must already exist on the target.
Consequences: Account creation (sign-up) is out of scope; login outcomes are reported separately from request stats.
Revisit if: provisioning is added as a separate, advanced phase.

## D-005: Tri-state verdict; zero completed requests never passes
Date: 2026-10-21
Status: accepted (imported)
Context: Early runs showed PASS at 100% errors, and stopped runs could pass with nothing measured.
Decision: PASS, FAILED, or "completed — no assertions configured". Zero completed requests is FAILED. Requests
cancelled by Stop are counted as `Cancelled`, not as errors.
Consequences: One verdict function feeds the UI and the CLI exit code.

## D-006: Ollama is an optional sidecar with a rules fallback
Date: 2026-10-21
Status: accepted (imported)
Context: Insight generation and request authoring benefit from a local model, but the core must run without one.
Decision: Compose profile `ai` starts Ollama. Only aggregates go to the model, URLs normalized, output grounded
against the input with tolerant numeric matching, and a rules analyzer is the fallback.
Consequences: The UI always says which analyzer produced a result.

## D-007: Docker image is distroless, pure-Go, non-root
Date: 2026-10-21
Status: accepted (imported)
Context: Single static image for self-hosting.
Decision: `CGO_ENABLED=0`, `modernc.org/sqlite`, `gcr.io/distroless/static:nonroot`, `/data` owned by uid 65532.
Consequences: No shell in the image; connectivity checks use a temporary curl container. `go test -race` needs gcc,
so it runs in the `golang:1.25` image (`scripts/race.*`) and in CI.

## D-008: Public docs are embedded explicitly; maintainer docs live beside them but are not shipped
Date: 2026-10-21
Status: accepted
Context: The kit keeps `progress.md`, `decisions.md`, `requirements.md` and `architecture.md` in `docs/`, but
`//go:embed *.md` would have embedded them in the shipped binary.
Decision: `docs/docs.go` embeds an explicit list of the public pages.
Consequences: A new public page must be added to both the embed list and `order`.

## D-009: Credential masking is a stored per-field flag, defaulted by whole-word names
Date: 2026-10-21
Status: accepted
Context: Masking by substring on the field name over-masked (`shipping`, `keyboard_layout`) and could not hide an
ordinary-named field (`nickname`). FR-CRED-01..04.
Decision: `CredentialPool.MaskedFields` (JSON `masked_fields`, no omitempty) lives in the pool spec. Import sets it
from `credential.DefaultMaskedFields` (whole-word match after splitting on separators and camelCase). The panel's
`POST /pools/{id}/masking` replaces it. `store.Migrate` gives pools without the key the old substring rule's fields,
once. A nil list (never stored) masks by that same old rule on read (`store.EffectiveMaskedFields`), so masking does
not change across the restart that migrates it.
Consequences: `GetCredentialPoolMasked` is the masked read; `GetCredentialPoolWithSecrets` stays the engine's only
unmasked read. Concurrent spec and masking saves each rewrite the whole payload, so one can undo the other (single user;
deferred).

## D-010: Compose defaults the analyzer to rules
Context: compose set `PRIMUS_ANALYZER=ollama` unconditionally, so without the `ai` profile insights started labelled
ollama then fell back, and authoring was shown but returned 502.
Decision: `PRIMUS_ANALYZER: ${PRIMUS_ANALYZER:-rules}`. AI needs `PRIMUS_ANALYZER=ollama` plus `--profile ai`.
Rejected: a startup reachability probe (no `depends_on`, so a slow Ollama would stay disabled until restart).
Consequences: no code change; `NewAnalyzer` still does no reachability check.

## D-011: Auth-probe pacing is a release ceiling; identity is chosen per run
Date: 2026-10-21
Status: accepted
Context: Step 8 wires attempts, rate and credentials into the auth-probe UI. FR-PROBE-01..04. Rule 3 says there is
no open-loop fixed req/s mode yet (step 9 owns it).
Decision: `TestConfig.MaxRate` (`max_rate`, attempts/sec) is a ceiling: `feed` releases each attempt at least
`1/max_rate` after the previous one was taken by a worker, over an unbuffered jobs channel. A slow stretch therefore
never turns into a catch-up burst that a sliding-window limiter would see. The rate actually reached is ≤ `max_rate`
and ≤ concurrency/latency. It is accepted for `auth_probe` only, from 0.001 up (smaller would overflow the
interval), and not together with a `burst_pattern`. A run's identity follows `auth_source`, and the running line
names it from the resolved auth (pool, global auth, or no auth). `TestConfig.AuthSource` (`auth_source`: empty | `global` | `pool` | `none`) picks the identity.
`engine.ResolveAuth` turns it into the `(AuthConfig, *CredentialPool)` that `RunWithPool` already takes, for the
web handler and the CLI alike. `applyAuth` stays the only dispatch path.
Rejected: a per-worker delay (the overall rate would depend on concurrency); a schedule anchored to the start
(`start + n/max_rate`), which bursts after a stall; username/password fields in the probe body (a second place to
store and mask secrets).
Consequences: step 9's open-loop mode is separate and must not reuse `max_rate` as its target rate.

## D-012: Open-loop rate mode releases on an absolute schedule into idle workers
Date: 2026-10-21
Status: accepted
Context: Step 9 adds a fixed req/s mode (FR-RATE-01..04). Closed-loop load hides a slow server: throughput drops to
concurrency/latency and latency excludes the queueing the user would have seen (coordinated omission). D-011 keeps
`max_rate` an auth-probe ceiling, so the target needs its own field.
Decision: `mode: rate` with `TestConfig.TargetRate` (`target_rate`, req/s). It needs `duration` and
`target_rate >= 0.01` (smaller overflows the due-time arithmetic); `target_rate` is rejected in other modes and with `max_rate` or `burst_pattern`. In rate mode
`concurrency` is the maximum number of requests in flight, never a rate. A scheduler replaces `feed` for this mode:
the n-th request is due at `start + n/target_rate` (anchored to the start, so it never drifts). At each due time it
takes one in-flight slot without blocking (one slot per worker left after pool logins, so `concurrency` at most; a semaphore that a worker frees once that request's
result is delivered) and queues the intended send time for the workers; with every slot busy the request is counted
as `Dropped`. A slot, not "is a worker blocked on receive right now", is the cap: the latter depends on goroutine
scheduling and dropped the very first request in testing. A late wake-up attempts every overdue slot at once, so nothing
is skipped silently. Workers stay the existing `runWorkers` (pools, re-login, payload pools), and a job carries its
intended send time; latency is measured from it. The summary adds `dropped`, `target_rate` and `achieved_rate`
(released requests over the duration, or the time until Stop). Any drop gives a new outcome, `fail_rate_not_sustained`, checked after
`fail_no_requests` and before the no-assertions rule; the CLI exits 1 on it.
Rejected: a goroutine per request behind a semaphore (a second worker path that pools and re-login would have to
follow); an unbounded queue in front of the workers (it hides drops as queueing); a `max_drop_rate` assertion (drops
would pass silently when no assertions are set).
Consequences: rule 3 in CLAUDE.md changes: open-loop exists, but only in rate mode. The start-anchored catch-up that
D-011 rejected for `max_rate` is intended here: an open-loop target must try to hold its schedule.
Revisit if: users need a tolerated drop share, or rates high enough that one scheduler goroutine cannot keep up.

## D-013: SQLite waits for locks and runs in WAL mode
Date: 2026-10-21
Status: accepted
Context: Step 9a. `store.OpenDB` opened SQLite with a bare path: no busy timeout and the rollback journal. A write
that overlapped a read on another pooled connection failed at once with `SQLITE_BUSY`. The insight goroutine logged
and dropped the error, so the insight stayed pending ("Generating...") until restart. The UI polls runs every 1s and
insights every 2s, so the overlap happens in normal use, and `SaveRun`, `FailInsight` and every other writer share
the exposure. The race build made it show as a 2-in-10 flake in `TestRunInsightAsyncLifecycleAndReuse`.
Decision: `OpenDB` appends `_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)` after `?`, or after `&` when the
path already has a query (modernc.org/sqlite applies `_pragma` on every pooled connection, `busy_timeout` first). Writes wait up to 5s for a lock, and readers no longer
block the writer. If saving a generated insight still fails, `generateRunInsight` marks it failed instead of leaving
it pending.
Rejected: `SetMaxOpenConns(1)` (serializes every read behind long writes and the polling handlers; the timeout and WAL
fix the failure without it); retrying in each writer (one setting in `OpenDB` covers them all).
Consequences: `primus.db-wal` and `primus.db-shm` sit beside the database. To back up, stop Primus, then copy the
whole `/data` directory: a live copy can catch the database and its `-wal` file at different moments, and
`primus.db` alone can miss recent writes. WAL needs shared memory on a local
filesystem, so `/data` must not be bind-mounted from a network share or a Windows or macOS host folder; use the
compose named volume. The README "Data and backups" section says so.
Revisit if: writes still time out under heavy load, or Primus must support `/data` on network storage.

## D-014: Run history query is a validated filter, interpreted in `insight`
Date: 2026-10-21
Status: accepted
Context: Step 10. Users want to ask the run history questions in plain language. The core must work without Ollama
(rule 6), and nothing secret may reach a model (rule 7).
Decision: The model (or, without it, a keyword parser) turns the question into a fixed JSON `Filter`. Primus validates
it strictly and applies it in memory over `store.ListAllRuns`. The model sees only the question, today's date and the
schema. The code lives in `internal/insight` as `query_*.go`, beside the analyzer config it reuses, so there is no new
package or commit scope. The Ollama call is synchronous on the request, capped at 20s, and falls back to rules on any
failure. The UI shows the interpretation and which interpreter ran.
Rejected: text-to-SQL (needs sandboxing and could reach credential columns); prose answers over run summaries (bounded
by context size, hard to verify); an async job through the registry (one bounded call does not need it).
Consequences: In-memory filtering loads every run on each query, which is fine at personal-tool scale.
Revisit if: history grows to tens of thousands of runs (move the filter into SQL over indexed columns).

## D-015: Run reports are rendered on request and redacted at render time
Date: 2026-10-21
Status: accepted
Context: Step 12. Users want to download and share a finished run. Stored error causes are Go transport errors, which
embed the full target URL including any query string (rule 7: credentials never leave in exports or error messages).
Decision: HTML, Markdown and CSV are rendered server-side from the stored run on each request and are not cached or
stored. URLs found in free text (error causes) are reduced to `scheme://host/path` when rendered, in the exports and in
the existing run page and result card, so runs stored before this change are covered too. The endpoint is named as
`METHOD scheme://host/path`. The code is `internal/web/report.go`; no new package or scope.
Rejected: redacting when the engine stores the cause (does not cover old runs, and loses detail in the database);
a JSON export and a CLI `--report` flag (not requested for this step); a PDF renderer (new dependency).
Consequences: Each download re-reads the run and its insight; the HTML report is a second copy of the page CSS.
Revisit if: reports need to be archived with the run or generated by `primus run`.

## D-016: Credential provisioning is a separate pool action, never a run phase
Date: 2026-10-21
Status: accepted
Context: Step 13 adds sign-up before testing. Today a pool only holds accounts that already exist on the target. Both
D-004 and CLAUDE.md rule 7 forbid account creation, so this decision changes both on purpose. Sign-up endpoints are
often rate-limited, need email verification, and leave data behind on the target. The engine has one login path:
`pool.go` sends a `LoginSpec` filled from `{{field}}` placeholders and reads the token with an `Extraction`.
Decision: A provisioning action on a pool, run by the user and separate from any run. It uses a sign-up template like
`LoginSpec`, plus generated placeholders (`{{seq}}`, and `{{secret}}` drawn from `crypto/rand`) and a count. Each
sign-up that succeeds adds a row to a new or existing pool, with the generated secret masked. An optional `Extraction`
can save a field from the response (for example `user_id`) into that row. Runs then use the pool exactly as they do now.
Rejected: a sign-up phase inside the run (every run leaves N more accounts, adds a third phase, mixes sign-up failures
and rate limits into the verdict, changes the engine and stored run JSON); an example fixture script only (no reuse of
the `Extraction` primitive, users write their own scripts).
Consequences:
- D-004's "account creation is out of scope" is replaced by this entry.
- Rule 7 becomes: "Primus creates accounts only through an explicit provisioning action the user starts, never during a run."
- Provisioning is capped at `MaxCredentialPoolRows` and at login concurrency. It runs asynchronously with
  `context.WithoutCancel`, and Stop cancels it.
- It reports attempted, succeeded and failed counts with failure reasons, like `LoginSummary`, and never shows secrets.
- Rows are stored only for sign-ups that return 2xx.
- Out of scope: deleting accounts on the target, email verification, captchas. `docs/credential-pools.md` must say so
  and stop saying "Primus never signs up".
- Watch for: a generated secret showing up in a log or error message, and partial pools after Stop.
Revisit if: users need fresh accounts on every run, or need provisioned accounts deleted after a run.

## D-017: Collections, seed data and per-endpoint reports
Date: 2026-10-21
Status: accepted
Context: Endpoints are a flat list. A new user starts with nothing to try, there is no per-endpoint summary, and
insights are a single free-text blob per run. Request URLs are stored as full `url_template` strings and used as-is
by `execRequest` and `Probe`, so a whole group of endpoints cannot be pointed at another host (such as an `httptest`
server) without editing each one.
Decision:
- A **collection** is a named set of endpoints. Every endpoint belongs to exactly one collection. A collection has an
  optional `base_url`; endpoint URLs may start with `{{base_url}}`, resolved at dispatch and probe time. This also lets
  tests point a whole collection at an `httptest` server, so no test touches the real network.
- Existing endpoints migrate into one non-built-in collection called "My endpoints". Nothing is lost.
- **Built-in** collections come from embedded JSON (`internal/seed`), seeded once on first run, identified by `seed_id`
  and `seed_version`. The seeder never modifies an existing collection. If the user deletes a built-in one, a tombstone
  row stops the seeder re-adding it on restart; only "Restore examples" brings it back.
- Deleting a collection removes its endpoints, their runs and insights in one transaction. Clearing history removes
  runs and insights only. Both refuse while any affected run is active, and both confirm with counts first.
- The per-endpoint report is **rule-generated**: numbers, trend table and the "how to read" key never come from the
  model. Ollama adds interpretation sections only, grounded against the same numbers, with the rules analyzer producing
  the same section shape as the fallback.
- UI is terminal-styled: monospace, text controls, no new CSS framework, no icon set, no JS beyond htmx.
Rejected: nested folders and moving endpoints between collections (out of scope, not needed for the seed); a local mock
target service (extra moving part; `base_url` plus `httptest` covers tests); letting the model write the numbers or the
key (cannot be trusted to quote them).
Consequences:
- New tables for collections and seed tombstones; `requests` gains a collection reference. No engine change beyond
  `{{base_url}}` resolution at the URL-building point.
- Runs and insights are keyed by test config and run ID with no foreign keys, so cascades are explicit SQL in one transaction.
- The seed targets a third-party service run by someone else: modest configs, documented in `docs/collections.md`.
- JSONPlaceholder has no auth, so the seed cannot demonstrate auth-probe or credential pools.
Revisit if: users need to move endpoints between collections, share or export collections, or want an authenticated demo target.

## D-018: Seeding runs at server start, never overwrites, and leaves moved rows alone
Context: C2 needed the built-in collection to appear without clobbering user edits or reappearing after a delete.
Decision: `seed.Ensure` runs only in server start (not `primus run`), so existing installs also get the collection on upgrade.
`SeedCollection` is one immediate transaction: skip if the seed ID exists, skip if tombstoned (unless restoring), fail with
`ErrDuplicateName` if a user collection holds the name. Seeded rows have fixed IDs (`seed-jp-<slug>`); on restore a row that already
holds such an ID (the user moved it to another collection) is left where it is. The restore handler takes no `scopeGuard`: it only inserts.
Revisit if: seed versions need upgrading in place, or more than one built-in collection ships.

## D-019: One SQLite connection per process; in-process callers wait in the pool
Context: SQLite's busy handler polls rather than queues, so back-to-back writers starved another connection past `busy_timeout` (calls blocked 4-5s; one `SQLITE_BUSY` in about 200 race runs). Provisioning writes many rows from an async job while the UI polls.
Decision: `store.OpenDB` sets `SetMaxOpenConns(1)`. Callers in this process wait in `database/sql`'s pool (unordered, no timeout, since store calls take no context), so they never reach the busy handler. Reads wait too. `busy_timeout` and `BEGIN IMMEDIATE` stay for a second process on the same file (`primus run`). Amends D-013, which assumed concurrent in-process readers.
Rules that follow: never call the `*sql.DB` while holding a connection, open rows or a transaction from it (it deadlocks); `inImmediateTx` rolls back on error and panic, and discards the connection if the rollback fails.
Revisit if: history grows enough that full-table reads (`ListAllRuns`) delay `SaveRun`; then read raw rows and unmarshal after closing, or add a separate read-only pool.
Follow-ups: `AppendCredentials` and `SaveCredentialPool` use deferred transactions and should move to `inImmediateTx`; store calls could take a context.
