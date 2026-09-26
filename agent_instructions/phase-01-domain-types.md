# Phase 1 — domain types + SQLite schema

## Goal

Establish the shared data model everything else builds on, and the
SQLite storage layer that persists it. No engine, no importer, no
web handlers yet — just types and durable storage.

## Scope

1. `internal/model`:
   - `RequestDefinition` — ID, Name, Method, URLTemplate, Headers
     (map[string]string), QueryParams (map[string]ParamSpec), Body
     (*BodyTemplate, JSON-serializable raw template), Auth
     (*AuthConfig, supports none/bearer/api-key/login-chained for
     later phases).
   - `ParamSpec` — Static string OR Dynamic *DynamicValue (random
     int range, random string, sequential).
   - `TestConfig` — RequestID, Mode (load/burst/auth_probe),
     Concurrency, TotalRequests, BurstPattern (*BurstPattern, count +
     idle, nil unless mode=burst), Assertions (ExpectedStatus []int,
     MaxP95Millis, MaxErrorRate).
   - `Run` — ID, TestConfigID, StartedAt, FinishedAt, Summary (byte
     status counts, p50/p95/p99, error rate), Passed bool.

2. `internal/store`:
   - `OpenDB(path string) (*sql.DB, error)` using
     `modernc.org/sqlite` (pure Go, no cgo — keeps the Docker build
     simple).
   - `Migrate(db *sql.DB) error` — creates tables for requests,
     test_configs, runs. Migrations must be idempotent (safe to run
     on an already-migrated DB).
   - CRUD functions: SaveRequestDefinition, GetRequestDefinition,
     ListRequestDefinitions, SaveTestConfig, GetTestConfig,
     SaveRun, GetRun, ListRuns.

## Follow the standing rules

Apply testing.md (TDD, AAA) to every function above — a failing test
before each one, not written in bulk afterward. Apply
session-logging.md after this phase's work is complete. Apply
git-commits.md for the phase-1 commit once acceptance conditions
below are all met.

## Acceptance conditions (must ALL be true before phase 1 is done)

- [ ] All four model types compile and round-trip through
      `encoding/json` (test: marshal, unmarshal, compare).
- [ ] `Migrate` run twice on the same DB produces no error and no
      duplicate tables/columns.
- [ ] Every CRUD function has a passing test using a temp SQLite
      file (not an in-memory-only DB, since the real deployment
      persists to disk).
- [ ] `go test ./... -race` passes with no failures or races.
- [ ] `go build ./...` succeeds.
- [ ] bob_sessions/ screenshot for this phase exists.
- [ ] One git commit exists for this phase, formatted per
      git-commits.md.

Do not begin Phase 2 (OpenAPI import) until every box above is
checked.
