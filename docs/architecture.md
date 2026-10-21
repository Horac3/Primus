<!-- mapped-at: f4cb472 2026-10-21 -->
# Architecture

## Purpose
Primus is a self-hosted API testing tool. The user defines requests (by hand, OpenAPI import, or natural language),
sets auth once, picks a mode (load, burst, soak, auth-probe), and gets live metrics, a pass/fail verdict and
optional LLM insights. Same engine behind an htmx UI and a headless CLI.

## Layout
```
cmd/primus/      main.go (flags, wiring, serve) · run.go (headless `primus run <config.json>`, exit 0/1/2)
internal/model/  shared types: RequestDefinition, TestConfig, Assertions, Run, RunSummary, CredentialPool, LoginSpec
internal/engine/ engine.go (entrypoints Run/RunWithSnapshot/RunWithPool, buildRun, Probe, applyAuth, params) ·
                 dispatch.go (stopPredicate, feed, workers, execRequest) · rate.go (open-loop `schedule`, mode: rate) ·
                 aggregate.go (aggregator goroutine, percentiles, histogram, series) · verdict.go (ValidateTestConfig,
                 assertions, DetermineOutcome) · probe.go (ResolveAuth) · pool.go (login phase, per-worker tokens) · provision.go (sign-up N accounts, D-016)
internal/store/  store.go (SQLite open, migrate, requests/configs/runs/auth/insights) · pools.go (credential pools)
internal/web/    web.go (routes + most handlers) · pools.go (pool panel) · registry.go (generic async job registry: runs, provisioning) · provision.go (pool provisioning) ·
                 report.go (html/md/csv run downloads, `redactURLs`, `endpointLabel`) · templates/ · static/
internal/insight/ analyzer.go (interface, rules fallback) · insight.go (summary, findings, grounding) · ollama.go ·
                 query_filter.go (history `Filter`: Validate/Apply/Describe) · query_rules.go (rules parser) ·
                 query_ollama.go (Ollama interpreter, 20s cap, rules fallback via `InterpretQuery`)
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
`verdict.go` (`EvaluateAssertions`, `DetermineOutcome`); zero completed requests is FAILED.
Run history can be queried in natural language: `GET /history-fragment?q=` interprets the question into an
`insight.Filter` (Ollama, rules fallback) and applies it to stored runs (D-014).

## Tests and tooling
`go test ./...` uses `httptest.Server` targets and temp SQLite files (`t.TempDir()`, `store.OpenDB`, `store.Migrate`).
`-race` needs gcc: use `scripts/race.sh`. Checks run via `.claude/kit/check.sh`. Known flake:
`TestProbe_SuccessfulTarget` (asserts latency > 0 on localhost).

## Build and deploy
`deploy/Dockerfile`: `golang:1.25` builder, `CGO_ENABLED=0`, distroless nonroot, `/data` for the DB.
CI is `.github/workflows/docker-publish.yml` (workflow `CI`): a `test` job (build, vet, gofmt, test, `-race`) on every
push and PR, and a `publish` job (`needs: test`) that pushes to Docker Hub only on `main`/`master` and `v*` tags.

## Hotspots
`internal/web/web.go` (1309 lines, most handlers) is still unsplit (roadmap step 14). `internal/engine/engine.go` was
split (now 259 lines). History in this clone is shallow, so churn data is unreliable.

## Planned
`tools/` (roadmap step 11, FR-META-01..04): local commit-message assistant. Dev tooling only; must not be imported by
`cmd/primus` or copied into the image. Not created yet.

## Known debt and contradictions
- `AGENTS.md`, `.bobignore`, `primus-scaffold-plan.md` and the root `web/` directory are Bob-era leftovers.
- `CONTRIBUTING.md` described a non-conventional commit format; updated to Conventional Commits.
- `deploy/docker-compose.yml` defaults `PRIMUS_ANALYZER` to `rules`; AI needs `PRIMUS_ANALYZER=ollama` plus
  `--profile ai`. There is still no `depends_on` for the Ollama service (left to the user).
- README still calls auth-probe "basic support"; check against `docs/auth-probe.md` before the next release.
