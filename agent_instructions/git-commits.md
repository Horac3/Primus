# Git commit discipline

The finished project ships as a single Docker container — keep that
in mind when naming things and structuring the /deploy folder, even
while a given phase's work is otherwise unrelated to packaging.

Each phase is exactly one git commit, made only after that phase's
acceptance conditions (see the active file in /agent_instructions/)
are fully satisfied — tests green, build succeeding, screenshot
logged per session-logging.md.

Commit message format:

```
<phase-tag>: <one-line summary>

- <specific change 1>
- <specific change 2>
- <specific change N>

Tests: <what was added/what it covers>
```

Example:

```
phase-1: add domain types and SQLite schema

- Add RequestDefinition, TestConfig, Run structs in internal/model
- Add SQLite schema + migration for requests, test_configs, runs tables
- Add store.OpenDB and store.Migrate in internal/store

Tests: table-driven tests for schema migration idempotency and
RequestDefinition JSON round-trip.
```

Do not squash multiple phases into one commit, and do not commit
mid-phase with a message like "wip" — if a phase must be paused,
leave it uncommitted rather than creating a commit that doesn't
represent a complete, tested phase.
