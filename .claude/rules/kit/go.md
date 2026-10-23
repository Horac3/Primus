---
paths:
  - "**/*.go"
  - "**/go.mod"
---

# Go conventions (claude-kit)

- Layout: `cmd/<app>/main.go` wires things up; code lives in `internal/`. Keep `main` thin.
- Accept interfaces, return structs. Declare interfaces where they are consumed, keep them small.
- `context.Context` is the first parameter of anything that does I/O or can block. Never store it in a struct.
- Errors: wrap with `fmt.Errorf("doing x: %w", err)`; compare with `errors.Is` / `errors.As`.
  Sentinel errors or typed errors for conditions callers act on. No `panic` outside `main` and init.
- No package-level mutable state; inject dependencies through constructors (`NewX(deps) *X`).
- Concurrency: whoever starts a goroutine owns its shutdown. Use `errgroup` and context cancellation.
- Tests: table-driven with `t.Run`, `t.Helper()` in helpers, `t.Parallel()` where safe,
  standard `testing` package unless the repo already uses a library. Fakes over mocks.
- `go test -race` needs cgo and a C compiler: run it in CI or Docker, not locally.
- Use `go get pkg@vX.Y.Z` (exact version) and commit `go.mod` and `go.sum`; never edit `go.sum` by hand.
- Run `gofmt` only on files you changed.
