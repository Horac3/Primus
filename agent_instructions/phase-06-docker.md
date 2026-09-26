# Phase 6 — Docker packaging

## Goal

The final deliverable ships as a single Docker container running the
Primus binary, serving the web UI, with the CLI mode also usable via
`docker run ... primus run ...`. No new application logic this
phase — just build/deploy plumbing around what already works.

## Scope

1. `/deploy/Dockerfile` — multi-stage build:
   - Stage 1 (builder): `golang:1.23` (or whatever version Phase 0
     pinned), `CGO_ENABLED=0` since `modernc.org/sqlite` is pure Go
     — this matters, don't let a dependency creep in that needs cgo,
     or the static binary/small final image breaks.
   - Compile `cmd/primus` into a single static binary.
   - Stage 2 (final): minimal base (`gcr.io/distroless/static` or
     `alpine` if you need a shell for debugging) — copy in the
     binary, the `web/templates` and `web/static` directories
     (embed them via `go:embed` instead if you want a truly single-
     file artifact with no separate asset directory to keep in
     sync — worth doing now since it removes a whole class of
     "forgot to copy templates" deploy bug).
   - `EXPOSE` the web port; `ENTRYPOINT` runs the web server by
     default, with the CLI subcommand reachable by overriding the
     container command.
   - A volume mount point for the SQLite DB file, so data survives
     container restarts (`VOLUME /data`, default `--db /data/primus.db`).

2. `/deploy/docker-compose.yml` — single service, volume for
   `/data`, port mapping, restart policy. Keep it to one service;
   there's no second container (no separate DB server, no separate
   frontend build step) given the architecture already decided.

3. `.dockerignore` — exclude `bob_sessions/`, `agent_instructions/`,
   `.git`, test files if you want a leaner build context (optional,
   test files compiling in doesn't hurt correctness, only build
   time).

## Follow the standing rules

No new business logic means limited scope for red/green unit tests
here, but the acceptance conditions below are the equivalent
discipline for this phase: a deploy artifact is either verified to
work or it isn't, so treat "build and run the container, then hit it
successfully" as this phase's non-negotiable check in place of a unit
test suite. Session-logging.md and git-commits.md apply as before.

## Acceptance conditions (must ALL be true before phase 6 is done)

- [ ] `docker build -f deploy/Dockerfile -t primus .` succeeds.
- [ ] `docker run --rm -p 8080:8080 -v primus-data:/data primus`
      starts cleanly, and the web UI is reachable at
      `http://localhost:8080`.
- [ ] Import the Petstore spec and run a small load test through the
      containerized instance — confirms templates/assets were
      actually copied/embedded correctly, not just that the binary
      compiled.
- [ ] Stop and restart the container; confirm previously imported
      request definitions and run history are still present (volume
      persistence actually works, not just declared).
- [ ] `docker run --rm primus run examples/petstore-load.json` (CLI
      path, overriding the default entrypoint command) exits with
      the expected code.
- [ ] `docker compose -f deploy/docker-compose.yml up` works as an
      equivalent one-command path.
- [ ] bob_sessions/ screenshot for this phase exists.
- [ ] One git commit exists for this phase, formatted per
      git-commits.md.

Do not begin Phase 7 (README/write-up) until every box above is
checked — this is the phase where "it built" and "it actually works
as a container" are easiest to conflate, so don't skip the manual
run-and-restart check for the sake of time.
