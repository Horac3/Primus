---
name: primus-soak
description: Soak-test an API with Primus - steady traffic for a long time on a remote server to catch slow degradation such as memory or connection leaks, growing latency, or tokens expiring mid-run. Use for soak, endurance or long-running stability tests.
---

# Soak test with Primus

A soak test holds ordinary traffic for a long time and watches for things that only show up over time:
latency creeping up, errors appearing after an hour, memory or connection leaks, caches or disks filling,
tokens expiring. It must run on a server (`primus-remote`), inside tmux, never from a laptop.
Non-production only.

## 1. Shape

- Traffic: normal production level, not peak (that is what load testing is for). Ask for it, or use
  about 50-70% of the capacity a load or stress test found.
- Length: at least 1 hour, more often 4-12. Primus's `soak` mode was built for minutes-scale runs and
  reports one aggregate per run, so long soaks run as a **chain of segments** (for example 12 runs of
  30 minutes, back to back). Each segment is saved and reported on its own, and the trend across segments
  is the result.
- A short baseline segment first (10 minutes), to compare against.

## 2. Config per segment

```json
{
  "id": "cfg-soak-orders",
  "request_id": "<endpoint id>",
  "mode": "soak",
  "concurrency": 10,
  "duration": 1800000000000,
  "assertions": { "expected_status": [200], "max_p95_millis": 400, "max_error_rate": 0.01 }
}
```

`duration` is in nanoseconds (30 min = `1800000000000`). `soak` is closed loop: `concurrency` workers.
For a fixed arrival rate, use a `rate` config with the same duration instead.

**Tokens.** A soak usually outlives access tokens. With global auth bearer tokens, make sure the token
lasts longer than the whole soak. With a credential pool, turn on **re-login on 401** for the pool (web UI,
see `primus-auth`), or every request after expiry fails with 401.

## 3. Run the chain on the server

`soak.sh` in the server's Primus directory, started with `remote-run.sh <host> soak-orders --exec soak.sh 12`:

```sh
#!/usr/bin/env bash
# soak.sh <segments> - run on the server from the Primus directory
set -u
n="${1:-12}"; dir=results/soak-$(date +%Y%m%d-%H%M%S); mkdir -p "$dir"
for i in $(seq 1 "$n"); do
  ./primus run configs/soak-orders.json --report-dir "$dir" --db primus.db >"$dir/segment-$i.json" 2>>"$dir/soak.log"
  code=$?
  echo "segment $i exit $code $(date -u +%FT%TZ)" >>"$dir/soak.log"
  [ "$code" -eq 2 ] && break    # config error or refused host: stop, nothing to learn
done
```

A failing segment (exit 1) does not stop the chain, because what happens after a bad segment is part of
the finding. Check progress with `remote-status.sh`. Stop early with `tmux send-keys -t primus-soak-orders C-c`.
The segment in progress is saved as stopped.

## 4. Read the trend

Fetch results (`fetch-results.sh`) and tabulate, per segment: p50, p95, p99, error rate, requests/sec,
status counts. Healthy is flat. Report any of these as a finding:

- p95 rising segment after segment (even inside budget) suggests something is accumulating.
- Errors starting late, especially 401s (token expiry), 503s (exhaustion) or timeouts.
- Throughput falling at constant concurrency, meaning latency is growing.

Ask the person for the server-side graphs (memory, connections, GC, disk) for the same window. A soak's
client-side numbers show *that* something degrades; server metrics show *what*. Write it up with
`primus-report`.
