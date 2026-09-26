# Phase 5 — CLI mode

## Goal

A headless entrypoint that runs the same engine as the web UI, for
CI use: `primus run <config.json>` exits 0 on pass, 1 on fail, and
prints a machine-readable summary. This should be cheap if Phases 1-3
kept the engine free of web-specific concerns — verify that
assumption first, don't just assume it held.

## Scope

1. `cmd/primus`: add a `run` subcommand (stdlib `flag` with a
   subcommand dispatch, or a tiny subcommand switch on `os.Args[1]`
   — no need for a CLI framework dependency this late, keep it
   simple).
   - `primus run <path-to-config.json> [--target <request-id>] [--db <path>]`
   - Loads the `TestConfig` from the JSON file (reuse
     `encoding/json` against the `model.TestConfig` struct directly
     — no new parsing logic).
   - Loads the referenced `RequestDefinition` from the SQLite store
     at `--db` (default path matching what the web server uses, so
     CLI and UI share the same imported definitions).
   - Calls `engine.RunLoad` or `engine.RunBurst` based on the
     config's mode — the exact same functions Phase 4's web handler
     calls. If this requires touching `internal/engine` itself
     rather than just calling it, that's a sign Phase 3/4 leaked a
     web-specific dependency into the engine — fix that instead of
     duplicating logic in the CLI.
   - Persists the `Run` via `store.SaveRun`, same as the web path,
     so CLI runs show up in the web UI's run history too.
   - Prints the summary as JSON to stdout (status counts, latency
     percentiles, error rate, pass/fail) — this is what a CI step
     would parse or archive as an artifact.
   - Exit code: 0 if `Run.Passed`, 1 otherwise. Exit 2 for a usage/
     config error (file not found, invalid JSON, unknown request ID)
     so CI can distinguish "test failed" from "toolkit
     misconfigured."

2. A sample config JSON (e.g. `examples/petstore-load.json`) checked
   into the repo, targeting a Petstore request definition, so the
   CLI is demoable without hand-writing a config live.

## Follow the standing rules

TDD/AAA per testing.md: test the exit-code logic and JSON output
shape directly (call the command's internal `run(args) int` function
in tests rather than shelling out to the compiled binary, which is
slower and harder to assert against). Session-logging.md and
git-commits.md apply as before.

## Acceptance conditions (must ALL be true before phase 5 is done)

- [ ] Test: valid config + passing run → exit code 0, JSON summary
      on stdout matches the persisted `Run`.
- [ ] Test: valid config + failing assertions → exit code 1.
- [ ] Test: missing config file → exit code 2, clear error message
      (not a panic/stack trace).
- [ ] Test: unknown request ID → exit code 2, clear error message.
- [ ] Manual check: `go build -o bin/primus ./cmd/primus && ./bin/primus run examples/petstore-load.json` produces the expected result against the real network.
- [ ] `go test ./... -race` passes.
- [ ] `go build ./...` succeeds.
- [ ] bob_sessions/ screenshot for this phase exists.
- [ ] One git commit exists for this phase, formatted per
      git-commits.md.

Do not begin Phase 6 (Docker packaging) until every box above is
checked.
