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
