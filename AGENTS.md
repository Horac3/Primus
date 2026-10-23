# AGENTS.md

## Purpose

This repo is a Go-based API load-testing toolkit called Primus. Most work should stay in the existing architecture: CLI entrypoints in `cmd/primus`, web UI and HTTP handlers in `internal/web`, engine behavior in `internal/engine`, persistence in `internal/store`, and shared models in `internal/model`.

For project context and feature details, start with:

- [README.md](README.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
- [docs/](docs/)

## Default workflow

- Keep changes small and scoped to one behavior at a time.
- Prefer existing patterns over adding new abstractions.
- When behavior is user-visible, verify via the app or the relevant `go test` target.
- Follow the repo’s TDD guidance: add the failing test first, make it pass with the smallest implementation, then refactor.

## Build and test

Use the repo’s standard checks before finishing work:

```sh
go build ./...
go test ./...
```

For engine concurrency work, use the repo’s recommended race check if the machine has a C compiler available; otherwise follow the Docker-based workaround in [CONTRIBUTING.md](CONTRIBUTING.md).

## Key code paths

- `cmd/primus/main.go` — binary entrypoint; serves the web UI by default and supports `run` mode for headless execution.
- `internal/web/` — HTTP handlers, embedded templates, and UI logic.
- `internal/engine/` — load/burst/soak behavior, assertions, percentile calculations, and result evaluation.
- `internal/store/` — SQLite setup and migrations.
- `internal/model/` — shared request/config/result data structures.
- `docs/` — per-mode reference docs that should be kept aligned with the behavior.

## Conventions

- Use idiomatic Go and the standard library unless the repo already uses a specific helper.
- Prefer table-driven tests for 3+ input variants.
- Keep each test focused on one logical assertion.
- Use `t.TempDir()`, `httptest.NewServer`, and other testing primitives already used in the repo, not custom harnesses.
- Do not add skipped tests or commented-out tests.
- Keep commit scope narrow; one logical change per commit/PR.

## When adding or changing features

- Reuse the current model and result flow instead of inventing parallel representations.
- For UI-facing work, check the relevant template and handler pair under `internal/web` and `web`.
- For engine behavior, inspect the matching tests under `internal/engine` before changing logic.
- For database or persisted state changes, ensure the migration/update path stays compatible with the existing SQLite setup.

## Important notes

- The web UI is embedded in the binary; no frontend bundler or generated assets are expected.
- The app can run either through Docker or directly with `go run ./cmd/primus`.
- The project intentionally avoids external services and keeps runtime simple.

## Links for deeper reading

- [README.md](README.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
- [docs/](docs/)
- [LICENSE](LICENSE)
