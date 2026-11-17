---
name: primus-remote
description: Install, configure and run Primus on a remote Linux server over SSH, keep long runs alive with tmux or nohup, open the web UI through an SSH tunnel, and copy results back. Use whenever a Primus test should run on a server rather than the local machine, or when setting up Primus somewhere new.
---

# Run Primus on a remote server

Load and soak tests belong on a machine close to the target with a steady network, not on a laptop.
This skill sets that machine up once, then runs every Primus command there. The other `primus-*`
skills describe *what* to run; this one is *how* it gets run.

## Layout on the server

Everything lives in one directory, `~/primus-tests` (override with `PRIMUS_HOME` in the scripts):

```
~/primus-tests/
  primus          the binary, or scripts/primus-docker.sh copied under this name
  primus.db       SQLite database: endpoints, configs, auth, runs (shared by CLI and web UI)
  primus.conf     KEY=value lines loaded as environment (no quotes, no secrets)
  specs/          OpenAPI specs to import
  configs/        test config JSON files
  results/        run JSON, Markdown reports, series CSV, logs
```

`primus.conf` holds settings, not secrets. Start with the production guard:

```
PRIMUS_PROTECTED_HOSTS=api.example.com,*.prod.example.com
```

Ask the person which hosts are production before the first heavy run, and add them here.

## 1. Install (once)

Check first: `ssh <host> '~/primus-tests/primus list --db ~/primus-tests/primus.db >/dev/null && echo ok'`.

**Binary (preferred, no Docker needed).** Build on any machine with Go 1.25 and copy it over:

```sh
git clone https://github.com/Horac3/Primus && cd Primus
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o primus ./cmd/primus   # arm64 servers: GOARCH=arm64
ssh <host> 'mkdir -p ~/primus-tests/{specs,configs,results}'
scp primus <host>:~/primus-tests/primus
```

**Docker.** On the server, build the image from the repo (`docker build -f deploy/Dockerfile -t primus:local .`),
or pull a published `<namespace>/primus:<version>` tag and set `PRIMUS_IMAGE`. Then copy
`scripts/primus-docker.sh` to `~/primus-tests/primus` and `chmod +x` it. The wrapper runs the image as
your user with `~/primus-tests` mounted at the same path, so every command below works unchanged.

## 2. Web UI through an SSH tunnel (for auth and pools)

The web UI has no login, so never publish its port on a server. Bind it to localhost and tunnel:

```sh
# on the server, in tmux so it outlives the SSH session
tmux new -d -s primus-ui 'cd ~/primus-tests && set -a && . ./primus.conf && set +a && ./primus --addr 127.0.0.1:8080 --db primus.db'
# on the person's machine
ssh -N -L 8080:127.0.0.1:8080 <host>     # then open http://localhost:8080
```

With Docker: `docker run -d --name primus-ui -p 127.0.0.1:8080:8080 -u "$(id -u):$(id -g)" -v ~/primus-tests:/work --env-file ~/primus-tests/primus.conf primus:local --addr :8080 --db /work/primus.db`.

The UI is needed only for things the CLI cannot do yet: setting global auth and managing credential
pools (see `primus-auth`). The person types secrets into the UI themselves; never ask for them in chat.

## 3. Get endpoints and configs onto the server

```sh
scp openapi.yaml <host>:~/primus-tests/specs/
ssh <host> 'cd ~/primus-tests && ./primus import specs/openapi.yaml --name "Orders API" \
  --base-url https://api.staging.example.com --default-config configs/smoke.json --db primus.db'
ssh <host> 'cd ~/primus-tests && ./primus list --collection "Orders API" --db primus.db'
```

`import` saves paths as `{{base_url}}/path`, so the collection's base URL is the one switch between
environments. `--default-config` gives every endpoint the same config; give individual endpoints their
own config files with `primus run configs/<file>.json` (that also saves the config for later collection
runs). Use the endpoint IDs that `list` prints as `request_id`.

## 4. Run

Short runs (smoke, a few minutes of load) can run in the foreground:

```sh
ssh <host> 'cd ~/primus-tests && set -a && . ./primus.conf && set +a && \
  ./primus run --collection "Orders API" --report-dir results/smoke-$(date +%F) --db primus.db'
```

Anything longer than a few minutes (soak, stress ladders) must survive a dropped SSH connection. Use the
scripts in this skill's `scripts/` folder from the person's machine:

```sh
scripts/remote-run.sh <host> soak-orders run configs/soak-orders.json --report-dir results/soak-orders
scripts/remote-status.sh <host> soak-orders      # still running, or "finished: exit N", plus the log tail
scripts/fetch-results.sh <host>                  # copies results/ to ./primus-results/<host>/
```

`remote-run.sh` starts the command in a tmux session (`primus-<name>`), or with `nohup` when tmux is
missing, and writes `results/<name>.json`, `.log` and `.exit`. To stop a run early, run
`ssh <host> 'tmux send-keys -t primus-<name> C-c'`, or `kill` the process. Primus treats Ctrl-C and SIGTERM
like the Stop button, so the partial run is still saved and reported.

Exit codes: `0` passed, `1` an assertion failed (or no assertions were set), `2` config error,
including a heavy run refused because its host is in `PRIMUS_PROTECTED_HOSTS`. Read the `.log` for the
reason before changing anything.

## 5. Bring results back

Run `fetch-results.sh`, then hand the files to `primus-report`. Results contain aggregates, status codes
and endpoint names. They never contain request bodies, headers, tokens or credentials, so they are safe to
copy. `primus.db` holds auth and credentials, so leave it on the server.

## Rules

- Confirm the target host and environment with the person before every heavy run. Never edit
  `PRIMUS_PROTECTED_HOSTS` to make a refused run pass; tell the person what was refused and why.
- One collection run at a time per database. The web UI and CLI share `primus.db`, which is safe, but do
  not clear history or delete collections in the UI while a CLI run is going.
- Note the server's location relative to the target (same region or across the internet) in every
  report, because it changes latency.
