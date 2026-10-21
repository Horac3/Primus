# CLI (`primus run`)

The headless entrypoint for CI: same engine as the web UI, machine-readable
output, exit codes a pipeline can gate on. Runs persist to the same SQLite
DB, so CLI runs show up in the web UI's history.

## Invocation

```sh
primus run <config.json> [--db <path>]
```

- `<config.json>` — a `TestConfig` JSON file (see [load](load.md) for the
  shape). The referenced `request_id` must already be in the DB: import
  the spec through the web UI first, or share the UI's DB file.
- `--db` — SQLite file, default `primus.db` (same default the server uses).
- Global auth comes from the same DB — set it in the UI once, or leave it
  unset for anonymous targets.
- `credential_pool_id` (optional) — log each worker in as its own account
  from a [credential pool](credential-pools.md) in the same DB, instead of
  global auth. The output then includes a `login` report (counts, login
  latency, failure classes; never credentials or tokens), and a run where
  no login succeeds exits `1` with outcome `fail_no_logins`.
- `auth_source` (optional) — `global`, `pool` or `none` picks the identity;
  empty keeps the rule above. `pool` without `credential_pool_id` exits `2`.
  For `auth_probe` runs, `max_rate` caps attempts per second. See
  [auth-probe](auth-probe.md).

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Run passed all assertions. |
| `1` | Run completed but an assertion failed (or every request failed). |
| `2` | Usage/config error: missing file, invalid JSON, failed validation, unknown `request_id`. |

## JSON output (stdout)

```json
{
  "id": "run-1790496283938613600",
  "test_config_id": "cfg-petstore-demo",
  "started_at": "2026-09-27T10:00:00Z",
  "finished_at": "2026-09-27T10:00:03Z",
  "summary": {
    "status_counts": { "200": 20 },
    "error_count": 0,
    "p50_millis": 120,
    "p95_millis": 480,
    "p99_millis": 900,
    "error_rate": 0.0,
    "total_requests": 20,
    "cancelled": 0
  },
  "passed": true
}
```

`total_requests` counts requests that got a response or a transport error.
`cancelled` counts requests that were still in flight when the run ended (a
cancelled CLI run, or a `duration` deadline). They are in no other field. See
"Cancelled requests" in [assertions](assertions.md).

`stderr` carries human-readable errors; `stdout` is always the JSON above
(on a completed run), so a pipeline step can parse or archive it.

## CI example

```yaml
- name: API load test
  run: |
    go run ./cmd/primus run ./examples/petstore-load.json --db primus.db
```

Exit `0` keeps the pipeline green; `1` fails the build on a real test
failure; `2` fails it on a misconfigured step — distinguishable at a
glance in the job log.

## Local Ollama service

The compose file runs Primus with the rules analyzer unless you set
`PRIMUS_ANALYZER`. A plain `docker compose up` needs no Ollama and shows no
AI features. From the `deploy/` directory, start Primus with the optional
Ollama service and switch the analyzer on in the same command:

```sh
PRIMUS_ANALYZER=ollama docker compose --profile ai up --build -d
docker compose exec ollama ollama pull qwen2.5:3b
```

In PowerShell, run `$env:PRIMUS_ANALYZER = "ollama"` first, then the same `docker compose` command without the prefix.

Set both. With the profile and no `PRIMUS_ANALYZER=ollama`, Ollama runs but Primus doesn't use it.
With `PRIMUS_ANALYZER=ollama` and no profile, Primus points at an `ollama` host that doesn't exist:
insights fall back to rules, and the authoring buttons fail when you use them.

For NVIDIA GPU access, install and configure the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html), then start with the GPU override:

```sh
PRIMUS_ANALYZER=ollama docker compose -f docker-compose.yml -f docker-compose.gpu.yml --profile ai up --build -d
```

The Ollama service keeps downloaded models in the `ollama-models` volume. Primus uses rules-based insights by default. With `PRIMUS_ANALYZER=ollama` and the `ai` profile active, it also enables Ollama-backed insights, natural-language request authoring, test-config authoring, and one-time sample-payload generation in the endpoint builder. Generated request and test-config details only pre-fill the form; they are not saved or run until you explicitly submit it. Payload variants are generated up front from the endpoint's JSON body template, capped at 500, and cycle through subsequent requests when the submitted run starts. Test-config suggestions are rejected if they exceed the default limits of 2,000 concurrent workers, one hour per timed run, or 1,000,000 requests per count-based run. These ceilings can be changed with `PRIMUS_AUTHOR_MAX_CONCURRENCY`, `PRIMUS_AUTHOR_MAX_DURATION_SECONDS`, and `PRIMUS_AUTHOR_MAX_TOTAL_REQUESTS`.
