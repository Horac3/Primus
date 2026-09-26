# Testing discipline

This project follows strict TDD: red, green, refactor. Apply this to
every unit of behavior, not just at the end of a feature.

## Workflow, every time
1. **Red** — write a failing test first, for the smallest next piece
   of behavior. Run it. Confirm it fails for the expected reason (not
   a compile error or typo).
2. **Green** — write the minimum code to make that test pass. Resist
   adding anything not required by the current test.
3. **Refactor** — with the test green, clean up naming, remove
   duplication, simplify. Re-run tests after every refactor step.
   Never refactor and add behavior in the same step.

Do not write implementation code before its test exists. Do not batch
multiple behaviors into one red/green cycle — one behavior per cycle.

## Test structure: Arrange-Act-Assert
Every test is written in three explicit blocks, separated by a blank
line (or `// Arrange` / `// Act` / `// Assert` comments in Go where
the block boundary isn't obvious from formatting alone):

```go
func TestWorkerPool_RespectsConcurrencyLimit(t *testing.T) {
    // Arrange
    pool := NewWorkerPool(5)
    jobs := makeJobs(20)

    // Act
    results := pool.Run(context.Background(), jobs)

    // Assert
    if got := maxConcurrent(results); got > 5 {
        t.Errorf("max concurrent = %d, want <= 5", got)
    }
}
```

No arranging inside the act block, no asserting inside setup helpers.
One logical assertion focus per test — table-driven tests are fine
for covering variants, but each table case still has one clear
behavior under test.

## Go-specific conventions
- Standard library `testing` package. Only reach for `testify` if a
  specific assertion genuinely needs it — don't add it by default.
- Table-driven tests for anything with 3+ input variants.
- Concurrency code (worker pool, aggregator) gets tests that
  specifically assert on the property that matters (max concurrency
  observed, no data races — run with `-race`), not just "it ran
  without crashing."
- No skipped or commented-out tests left in the codebase. A test
  that can't run yet doesn't get written yet.
