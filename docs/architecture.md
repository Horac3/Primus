<!-- mapped-at: 6e20275 2026-10-21 -->
# Architecture

## Purpose
Primus is a self-hosted API testing tool. The user defines requests (by hand, OpenAPI import, or natural language),
sets auth once, picks a mode (load, burst, soak, auth-probe), and gets live metrics, a pass/fail verdict and
optional LLM insights. Same engine behind an htmx UI and a headless CLI.

## Layout
```
cmd/primus/      main.go (flags, wiring, serve) · run.go (headless `primus run <config.json>`, exit 0/1/2)
internal/model/  shared types: RequestDefinition, TestConfig, Assertions, Run, RunSummary, CredentialPool, LoginSpec
internal/engine/ engine.go (dispatch loop, aggregator, verdict, Probe, applyAuth) · pool.go (login phase, per-worker tokens)
internal/store/  store.go (SQLite open, migrate, requests/configs/runs/auth/insights) · pools.go (credential pools)
internal/web/    web.go (routes + most handlers) · pools.go (pool panel) · registry.go (async run registry) · templates/ · static/
internal/insight/ analyzer.go (interface, rules fallback) · insight.go (summary, findings, grounding) · ollama.go
internal/author/ natural-language request and test-config generation, payload pools, bounds (uses Ollama)
internal/credential/ credential types, CSV/JSON import, extraction helpers
internal/importer/ OpenAPI 2/3 import (secondary path)
docs/            public reference pages (embedded, served at /docs) + maintainer files (progress, decisions, ...)
deploy/          Dockerfile (distroless), docker-compose.yml (profile `ai` adds Ollama), docker-compose.gpu.yml
web/             old Bob-era copy of templates/static, not used by the app (known stray)
```

## How a request flows
1. `POST /runs` (`postRunHandler`) validates the config, loads the endpoint, global auth and active pool, and starts a
   run in the registry (`registry.go`) using `context.WithoutCancel` on the request context.
2. The registry runs `engine.RunWithPool` (or `RunWithSnapshot`). With a pool, the login phase resolves one token
   per worker first, reported separately. `feed` releases work, `runWorkers` fire requests (`execRequest`), and one
   `aggregate` goroutine owns counters, percentiles, histogram buckets and the time series.
3. The browser polls `GET /runs/{id}/status` every second for a live snapshot; `POST /runs/{id}/stop` cancels the context.
4. On completion the verdict (`DetermineOutcome`) is computed, the run is stored, and the result card is rendered
   (`run-result-fragment.html`). `POST /runs/{id}/insights` produces rules or Ollama insights.
5. `primus run` calls the same engine without the registry and maps the verdict to the exit code.

## Domain and data
SQLite tables: `requests`, `test_configs`, `runs`, `global_auth` (singleton), `insights`, `credential_pools`,
`credentials`, `active_credential_pool` (singleton). Runs, summaries and series are stored as JSON.
Auth is applied at dispatch by `applyAuth`; a header value `[GLOBAL]` pulls in global auth. Verdict logic lives in
`engine.go` (`EvaluateAssertions`, `DetermineOutcome`); zero completed requests is FAILED.

## Tests and tooling
`go test ./...` uses `httptest.Server` targets and temp SQLite files (`t.TempDir()`, `store.OpenDB`, `store.Migrate`).
`-race` needs gcc: use `scripts/race.sh`. Checks run via `.claude/kit/check.sh`. Known flake:
`TestProbe_SuccessfulTarget` (asserts latency > 0 on localhost).

## Build and deploy
`deploy/Dockerfile`: `golang:1.25` builder, `CGO_ENABLED=0`, distroless nonroot, `/data` for the DB.
CI today is `.github/workflows/docker-publish.yml` only (builds and pushes to Docker Hub on `main`/`master` and `v*`
tags). It does not run tests or the race detector yet (roadmap step 6).

## Hotspots
`internal/web/web.go` (1218 lines, most handlers) and `internal/engine/engine.go` (729 lines). History in this clone
is shallow, so churn data is unreliable. Both files are candidates for splitting by concern once a step touches them.

## Known debt and contradictions
- `AGENTS.md`, `.bobignore`, `primus-scaffold-plan.md` and the root `web/` directory are Bob-era leftovers.
- `CONTRIBUTING.md` described a non-conventional commit format; updated to Conventional Commits.
- `deploy/docker-compose.yml` sets `PRIMUS_ANALYZER: ollama` on the app service, and has no `depends_on` for the
  `ai`-profile Ollama service. Check what happens when the profile is off.
- README still calls auth-probe "basic support"; check against `docs/auth-probe.md` before the next release.
