# Global auth

One auth configuration for the whole app, applied to every dispatched
request (probe, load, burst, soak, CLI). Set it once in the top auth bar
instead of re-entering credentials per endpoint — switching sidebar
endpoints never asks for auth again.

## Supported types

| Type | Sends | Fields |
|---|---|---|
| `none` | nothing | — |
| `bearer` | `Authorization: Bearer <token>` | token |
| `api_key` | `<header>: <value>` | header name (default `X-API-Key`), value |
| `basic` | HTTP Basic for username/password | username, password |

(`login_chained` exists in the config shape but nothing acts on it yet —
accepted, ignored at dispatch. Roadmap.)

Stored as a single row in SQLite (`global_auth`), so it survives restarts
and applies to CLI runs against the same DB file.

## Per-endpoint override (`[GLOBAL]`)

An endpoint's builder has an Authorization header row. Its value works
like this:

- Empty or `[GLOBAL]` → global auth applies (the default).
- Anything else → that literal value is sent instead, and global auth is
  skipped for that endpoint. The ✕ button clears the row back to global.

Use it for the one endpoint that needs a different token without
disturbing everything else.

## Credential pools

To simulate many users instead of one shared identity, activate a
[credential pool](credential-pools.md) in the auth bar. An active pool
replaces global auth for runs. Each worker logs in as its own account, and
a `[GLOBAL]` header row then receives that worker's token.
