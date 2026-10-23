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
- **auth-probe** — repeated credentialed requests, for rate-limit/lockout behavior (attempt count, max attempts/sec and the identity to send as).

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

## Data and backups

- SQLite runs in WAL mode, so Primus keeps `primus.db-wal` and `primus.db-shm`
  beside `primus.db`. Recent writes can live in the `-wal` file until SQLite
  folds them back in.
- To back up, stop Primus, then copy the whole `/data` directory (or the
  directory holding `primus.db` without Docker). A copy taken while Primus runs
  can catch the database and its `-wal` file at different moments, and copying
  `primus.db` alone can miss recent writes.
- WAL needs shared memory on a local filesystem. Bind-mounting `/data` from a
  network share, or from a Windows or macOS host folder, can break it. Use the
  named volume that compose creates.

## Docs

- [`docs/`](docs/) — one reference file per test mode, plus global auth,
  assertions, and the CLI. The same files are viewable inside the app at
  `/docs`.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — dev setup, testing discipline,
  commit convention, PR checklist.

## What's next (roadmap, not bugs)

- **True multi-hour soak**: soak is demo-scale today (seconds-to-minutes)
  with no trend graphing across runs.

## License

MIT — see [LICENSE](LICENSE).
