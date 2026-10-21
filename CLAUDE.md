# CLAUDE.md — Primus

Loaded every session, so every line costs tokens on every turn. Generic rules live in
`.claude/rules/kit/` (loaded automatically; Go, Docker and frontend rules load only when matching files are
touched). Primus-specific path rules live in `.claude/rules/`. Do not repeat them here.

Read on demand, only the parts the task needs:
- `docs/progress.md`: roadmap, current step, handoff notes. The session's source of truth.
- `docs/requirements.md`: requirement IDs for planned work. Grep by ID; do not read it whole.
- `docs/decisions.md`: decision log. Check before contradicting an earlier choice.
- `docs/architecture.md`: codebase map. Read before exploring.
- `docs/*.md` (load, burst, soak, auth-probe, global-auth, credential-pools, assertions, cli) are the **public**
  user reference pages, embedded in the binary and served at `/docs`. Keep them true to the behaviour.
- `agent_instructions/` (gitignored, may be absent): private phase prompts. A roadmap step may name one as its spec.

## Product
Primus is a self-hosted API testing tool: load, burst, soak and auth-probe tests with a Postman-style request
builder, live metrics, credential pools (many distinct authenticated users), pass/fail assertions, and an
optional local-LLM analyzer. Web UI (htmx) or headless CLI (`primus run`) for CI. One Go binary, SQLite, one
Docker image. Open source (MIT). Module `github.com/Horac3/Primus`. Started as an IBM Bob 2.0 hackathon
project; now a personal open-source tool.

## Stack
Go 1.25, htmx (embedded, no bundler), SQLite via pure-Go `modernc.org/sqlite`, `html/template`, goldmark for
docs. Layout: `cmd/primus` (entrypoint, `run` subcommand), `internal/{engine,model,store,web,insight,author,
credential,importer}`, `docs/` (public pages), `deploy/` (Dockerfile, compose), `examples/`.
Optional Ollama sidecar (compose profile `ai`) for `insight` and `author`.

## Commands
All checks go through `.claude/kit/check.sh` (configured in `.claude/kit/commands.env`), ideally via the
`test-runner` subagent: `all` · `typecheck` · `lint` · `test` · `test-one "<pattern>"`.
- Race detector: `scripts/race.sh` (or `scripts/race.ps1`). It runs `go test -race ./...` inside the
  `golang:1.25` image because the dev machine has no gcc. CI also runs it. Run it whenever goroutine code changed.
- Run locally: `go run ./cmd/primus --addr :8080 --db primus.db`, or `docker compose -f deploy/docker-compose.yml up`
  (`--profile ai` adds Ollama).

## Architecture rules (non-negotiable)
1. **htmx, not React.** Forms and swapped fragments; plain inline SVG for charts. No JS framework, bundler or build step.
2. **One engine loop.** `engine.Run*` serves load, burst and soak. Stop condition is `TotalRequests` XOR `Duration`.
   Runs are async through the run registry with 1s polling; Stop cancels the run's context. Work started from an
   HTTP handler must use `context.WithoutCancel`, because the request context dies when the handler returns.
3. **Closed-loop load.** Concurrency is not requests/sec: throughput = concurrency / latency. The only open-loop
   path is `mode: rate` (D-012): `target_rate` is the rate and concurrency is the in-flight cap. Never describe
   concurrency as a rate.
4. **Auth.** One singleton `global_auth` row, applied at dispatch by `applyAuth`; a header set to `[GLOBAL]` uses it.
   An active credential pool overrides global auth for the run through the same `applyAuth` path. No parallel path.
5. **Verdict is tri-state:** PASS, FAILED, "completed — no assertions configured". A run with zero completed
   requests never passes. Requests cancelled by Stop are counted as `Cancelled`, never as errors.
6. **Ollama is optional.** The core never depends on it; a rules analyzer is the fallback and the UI says which
   analyzer ran. Only aggregate summaries and findings go to a model: no bodies, credentials or tokens.
7. **Credentials are secrets.** Masked in the UI after saving; never in logs, run JSON, exports, error messages or
   analyzer payloads. Pools use pre-existing accounts only; Primus does not create accounts on a target.
8. **Docker:** `CGO_ENABLED=0`, distroless `nonroot`, no shell in the final image, `/data` owned by uid 65532.

## Commit scopes
`engine`, `model`, `store`, `web`, `ui`, `insight`, `author`, `auth`, `importer`, `cli`, `docs`, `docker`, `ci`.
Body: bullets for each specific change, then a final `Tests:` line saying what the tests cover.

## Project-specific working rules
- `go test -race` is not runnable locally; use `scripts/race.sh`. Do not skip it for engine, registry or pool changes.
- Tests: `httptest.Server` as the target, never the real network. `httptest.NewRequest` contexts never cancel,
  so async-lifecycle tests need a real server round trip. Concurrency tests assert the property (max in-flight,
  exactly-once persistence, prompt cancellation).
- User-visible changes update the matching `docs/<mode>.md` page in the same step.
- `docs/docs.go` embeds an explicit list of public pages. If you add a public page, add it to the embed list and
  to `order`. Maintainer files (`progress.md`, `decisions.md`, `requirements.md`, `architecture.md`) must never be embedded.
- `AGENTS.md` and the root `web/` directory are old Bob-era leftovers. Leave them alone unless asked.
- The user rewrites git history by hand (`git filter-repo`, interactive rebase). Never do it, even if asked in passing.
- `deploy/docker-compose.yml` is the public quickstart and publishes port 8080 on purpose. The kit's
  `nginx-proxy` production rule does not apply to it.

## Workflow
- Start every conversation with `/next`. One roadmap step per conversation; `/clear` between steps.
- Plan in plan mode (Opus via `opusplan`), build on Sonnet, delegate checks to `test-runner`, review with `/review`
  (add `concurrency-reviewer` when goroutine code changed), hand off with `/handoff` before context reaches ~60%.

## Compact instructions
When compacting, keep: the current step and its requirement IDs, test status (pass/fail and failing
test names), files changed, decisions made, and the exact next action. Drop: full test logs, file
contents already written to disk, exploration that did not lead anywhere.
