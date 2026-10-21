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
Status: accepted (imported)
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
`target_rate > 0`; `target_rate` is rejected in other modes and with `max_rate` or `burst_pattern`. In rate mode
`concurrency` is the maximum number of requests in flight, never a rate. A scheduler replaces `feed` for this mode:
the n-th request is due at `start + n/target_rate` (anchored to the start, so it never drifts). At each due time it
takes one of `concurrency` in-flight slots without blocking (a semaphore that a worker frees once that request's
result is delivered) and queues the intended send time for the workers; with every slot busy the request is counted
as `Dropped`. A slot, not "is a worker blocked on receive right now", is the cap: the latter depends on goroutine
scheduling and dropped the very first request in testing. A late wake-up attempts every overdue slot at once, so nothing
is skipped silently. Workers stay the existing `runWorkers` (pools, re-login, payload pools), and a job carries its
intended send time; latency is measured from it. The summary adds `dropped`, `target_rate` and `achieved_rate`
(released requests per measured second). Any drop gives a new outcome, `fail_rate_not_sustained`, checked after
`fail_no_requests` and before the no-assertions rule; the CLI exits 1 on it.
Rejected: a goroutine per request behind a semaphore (a second worker path that pools and re-login would have to
follow); an unbounded queue in front of the workers (it hides drops as queueing); a `max_drop_rate` assertion (drops
would pass silently when no assertions are set).
Consequences: rule 3 in CLAUDE.md changes: open-loop exists, but only in rate mode. The start-anchored catch-up that
D-011 rejected for `max_rate` is intended here: an open-loop target must try to hold its schedule.
Revisit if: users need a tolerated drop share, or rates high enough that one scheduler goroutine cannot keep up.
