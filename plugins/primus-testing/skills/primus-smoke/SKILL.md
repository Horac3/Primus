---
name: primus-smoke
description: Smoke-test an API with Primus after a deploy or before heavier tests - every critical endpoint gets a few sequential requests with status-code and latency assertions, run as one collection. Use for "is the API up", post-deploy checks, or a quick sanity pass.
---

# Smoke test with Primus

A smoke test answers one question fast: does every critical endpoint answer, with the right status,
in reasonable time? It runs before any heavy test (no point loading a broken API) and after every
deploy. It is light enough to run against production: Primus lets count-based runs of at most 2
workers and 50 requests through even when the host is in `PRIMUS_PROTECTED_HOSTS`.

Run everything on the server with the `primus-remote` skill.

## 1. Choose the endpoints

Pick the critical paths: health, the main read endpoints, and any read that touches each backing
service (database, cache, a downstream API). Use read-only calls only. A smoke run against production
must never create, change or delete data. Writes belong in a staging smoke run, if at all.

Negative cases count too: an endpoint that must reject a missing token (expect 401) or an unknown id
(expect 404) can be its own endpoint in the collection with its own `expected_status`.

## 2. The config

`configs/smoke.json`:

```json
{
  "mode": "load",
  "concurrency": 1,
  "total_requests": 3,
  "assertions": {
    "expected_status": [200],
    "max_p95_millis": 1000,
    "max_error_rate": 0.0001
  }
}
```

- `total_requests`, not `duration`: count-based is what makes the run light.
- `max_error_rate` uses a tiny value because `0` means "off" in Primus. With `expected_status` set, any
  other status already fails the run.
- Pick `max_p95_millis` from the API's agreed latency budget. Without one, use 1000 ms and say that
  it is a placeholder.
- Every smoke config has assertions. A run with none ends "completed - no assertions" and exits `1`.

## 3. Set it up and run

```sh
# once: import the spec with the smoke config for every endpoint
./primus import specs/openapi.yaml --name "Orders API smoke" --base-url https://api.staging.example.com \
  --default-config configs/smoke.json --db primus.db
# endpoints that need another status: their own config file, run once to save it
./primus run configs/smoke-orders-unknown-id.json --db primus.db      # expected_status [404]
# every time
./primus run --collection "Orders API smoke" --report-dir results/smoke-$(date +%F-%H%M) --db primus.db
```

Remove endpoints that should not be smoke-tested (writes, slow reports) from the collection in the web
UI, or give the smoke collection only the endpoints you want. Importing the spec again creates a fresh
copy, so it never disturbs this collection.

Auth: endpoints use the server's global auth. Set it once in the web UI through the SSH tunnel (see
`primus-auth`).

## 4. Read the result

Exit `0`: every run passed. Exit `1`: at least one endpoint failed or had no assertions. Exit `2`: an
endpoint could not run (no base URL, refused by the protected host guard, invalid config). The JSON on
stdout lists every endpoint with `passed`, `failed`, `no_assertions`, `skipped` or `error`. Report
failures by endpoint name with the status codes seen (`summary.status_counts`) and p95. Hand the
results folder to `primus-report` for a write-up.

## In CI

The same collection run works as a pipeline step after deploy. It fails the job on exit 1 or 2. Keep the
database on the runner's cache or point `--db` at a file checked out with the configs. See the
"CI example" in Primus's `docs/cli.md`.
