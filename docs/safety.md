# Protected hosts

Primus can refuse to send heavy traffic to hosts you mark as protected, usually
production. Set the list on the machine that runs Primus (the server for the web
UI, the shell for `primus run`):

```sh
PRIMUS_PROTECTED_HOSTS="api.example.com,*.prod.example.net"
```

Entries are exact hostnames or `*.domain` patterns. A pattern covers the domain
itself and every subdomain: `*.prod.example.net` protects `prod.example.net` and
`eu.prod.example.net`, but not `notprod.example.net`. Matching ignores case and
port. With the variable unset, nothing is protected and Primus behaves as before.

## What may still run

A **light** run may reach a protected host without an override: a `load` run
with `total_requests` (not `duration`), at most 2 workers and at most 50
requests. That is enough for a smoke check after a deploy. Change the limits
with:

| Variable | Default | Meaning |
|---|---|---|
| `PRIMUS_PROTECTED_MAX_CONCURRENCY` | `2` | Most workers a light run may use. |
| `PRIMUS_PROTECTED_MAX_REQUESTS` | `50` | Most requests a light run may send. |

Every other run is **heavy** and is refused: duration-based load, burst, soak,
fixed rate and auth-probe, and any load run over the limits. The web UI's load
mode is duration-based, so from the UI only an override lets a run reach a
protected host.

## The override

If you own the system and mean to load it, name the host exactly:

- in a test config: `"allow_protected_host": "api.example.com"`
- in the web UI: **Protected host override** under the run settings
- on the CLI: `primus run ... --allow-protected-host api.example.com`, which
  applies to every config in the call that leaves the field empty

The override must equal the target's hostname, so a config written for one
host cannot be pointed at another. A wildcard is not accepted as an override.

## What a refusal looks like

The web UI answers `400` and names the host, the limits and the override. The
CLI prints the same message on stderr and exits `2`, the usage/config code, so
a pipeline shows it as a misconfigured step. A refused run is not started and
not saved. In [Run all](collections.md) a refused endpoint is listed as skipped
with that reason, and the sequence moves on.

If protected hosts are configured and an endpoint's URL has no readable host,
the run is refused rather than guessed at.
