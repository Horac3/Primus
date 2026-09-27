# Primus

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Primus is a self-hosted API testing toolkit: point it at any HTTP API, pick a
test mode, and get pass/fail verdicts with latency percentiles — through an
htmx-driven web UI or a headless CLI built for CI. One Go binary, SQLite for
storage, no external services.

Test modes, one line each:

- **load** — steady traffic: N workers firing requests for a fixed count or a fixed duration.
- **burst** — pulsed traffic: requests released in bursts with idle gaps, for recovery testing.
- **soak** — duration-only steady traffic, for catching slow degradation over minutes.
- **auth-probe** — repeated credentialed requests, for rate-limit/lockout behavior (basic support today, see roadmap below).

## Quickstart

```sh
docker compose -f deploy/docker-compose.yml up --build
```

Open <http://localhost:8080>, import an OpenAPI spec (or add an endpoint by
hand), set global auth once, and run a test. Data persists in a Docker volume
at `/data/primus.db`.

Without Docker:

```sh
go run ./cmd/primus            # serve the UI on :8080, DB in ./primus.db
go run ./cmd/primus run <config.json>   # headless CI run, exits 0/1/2
```

## Docs

- [`docs/`](docs/) — one reference file per test mode, plus global auth,
  assertions, and the CLI. The same files are viewable inside the app at
  `/docs`.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — dev setup, testing discipline,
  commit convention, PR checklist.

## What's next (roadmap, not bugs)

- **Auth-probe UI wiring**: the mode exists and runs, but dedicated
  attempt-count/rate/credential fields are not in the UI yet.
- **True multi-hour soak**: soak is demo-scale today (seconds-to-minutes)
  with no trend graphing across runs.

## License

MIT — see [LICENSE](LICENSE).
