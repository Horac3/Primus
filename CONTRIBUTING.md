# Contributing to Primus

## Dev setup

```sh
git clone <repo-url>
cd primus
go build ./...
go test ./...
```

That's it — no codegen, no bundler, no external services. The web UI's
templates and static assets are embedded in the binary; the frontend
libraries (htmx) load from CDN.

To run the full stack locally for manual verification:

```sh
# Option A: Docker (same image as production)
docker compose -f deploy/docker-compose.yml up --build
# then open http://localhost:8080

# Option B: plain go, SQLite file in the repo dir (gitignored)
go run ./cmd/primus --addr :8080 --db primus.db
```

## A note on `go test -race`

The race detector needs a C compiler (`cgo`), which stock Windows and
minimal Linux installs don't have — `go test -race` will fail there with
`C compiler "gcc" not found`, and that failure is about your machine, not
the code. Anything touching `internal/engine` concurrency should still be
race-checked before merge; run it inside the Go Docker image instead:

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.25 go test -race ./...
```

(On Windows PowerShell, replace `$PWD` with `${PWD}`.)

## Docker Hub publishing

The `Publish Docker image` GitHub Actions workflow publishes images to
`DOCKERHUB_NAMESPACE/primus`. Configure these in the repository's **Settings
→ Secrets and variables → Actions** before merging the workflow:

- Secret `DOCKERHUB_USERNAME`: Docker Hub account used to authenticate.
- Secret `DOCKERHUB_TOKEN`: Docker Hub access token with permission to push.
- Variable `DOCKERHUB_NAMESPACE`: Docker Hub user or organization that owns
  the `primus` repository. Create that repository on Docker Hub first.

Each push to `master` or `main` publishes `0.1.0-beta.<workflow run number>`.
To publish a stable release, push a version tag such as `v0.1.0`; the workflow
publishes `0.1.0` and updates `latest`. A version tag containing a prerelease
suffix, such as `v0.1.0-beta.4`, publishes that exact prerelease tag without
updating `latest`.

## Testing discipline (TDD)

We practice strict test-driven development, one small behavior at a time:

1. **Red** — write a failing test first, for the smallest next piece of
   behavior. Run it. Confirm it fails for the expected reason (not a
   compile error or typo).
2. **Green** — write the minimum code to make that test pass. Resist
   adding anything not required by the current test.
3. **Refactor** — with the test green, clean up naming, remove
   duplication, simplify. Re-run tests after every refactor step. Never
   refactor and add behavior in the same step.

Test conventions:

- Every test follows Arrange–Act–Assert as blank-line-separated blocks
  (or `// Arrange` / `// Act` / `// Assert` comments where the boundary
  isn't obvious).
- One logical assertion focus per test. Table-driven tests for 3+ input
  variants.
- Standard-library `testing` only — no assertion frameworks.
- Concurrency tests assert the *property* (max concurrency observed,
  exactly-once persistence, prompt cancellation), not just "didn't
  crash". HTTP targets use `httptest.NewServer`; databases use a temp
  file (`t.TempDir()` + `store.OpenDB` + `store.Migrate`).
- No skipped or commented-out tests. A test that can't run yet doesn't
  get written yet.

## Commit convention

One logical change per commit (and per PR — don't bundle unrelated work):

```
<scope>: <one-line summary>

- <specific change 1>
- <specific change 2>

Tests: <what was added / what it covers>
```

## PR checklist

- [ ] `go build ./...` succeeds.
- [ ] `go test ./...` passes (and `-race` for engine concurrency changes —
      see the Docker workaround above if your machine lacks gcc).
- [ ] One logical change; commit message follows the convention above.
- [ ] Manually verified in the running app where the change is user-
      visible (`docker compose up` or `go run ./cmd/primus`).
