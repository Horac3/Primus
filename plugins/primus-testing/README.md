# primus-testing: Claude skills for Primus

A Claude Code plugin that lets Claude plan, run and report API tests with
[Primus](../../README.md), typically on a remote server. It holds only skills and small
helper scripts. All testing is done by Primus itself.

## Install

```text
/plugin install primus-testing --marketplace Horac3/primus
```

On older Claude Code versions: `/plugin marketplace add Horac3/Primus`, then
`/plugin install primus-testing@primus`.

## Skills

| Skill | Use it for |
|---|---|
| `primus-test-plan` | Turning "test this API" into a plan; maps every test type to Primus and says what is not supported yet |
| `primus-remote` | Installing Primus on a Linux server, the SSH-tunnelled web UI, long runs in tmux/nohup, fetching results |
| `primus-smoke` | Post-deploy checks: a few requests per endpoint, run as one collection |
| `primus-load` | Latency and capacity at a target rate (`rate` mode) or worker count (`load` mode) |
| `primus-stress` | A rate ladder that finds the breaking point (`scripts/stress-ladder.sh`) |
| `primus-spike-burst` | Pulsed bursts and before/spike/after runs, to check recovery |
| `primus-soak` | Long runs as chained segments, watching for slow degradation |
| `primus-auth` | Global auth, credential pools, and auth-probe rate-limit and lockout tests |
| `primus-regression` | Baseline vs current collection runs (`scripts/compare_runs.py`), CI gate |
| `primus-report` | Reading run JSON, Markdown reports and series CSV into a report |

Functional (body checks), integration (chained calls), fuzz and wider security testing need Primus
features that are on the roadmap (`docs/progress.md`, T1-T6). The test-plan skill says so rather than
faking them. UI testing is out of scope: Primus is API-only.

## Safety built in

- Heavy runs (load, stress, spike, burst, soak, auth-probe) are refused against hosts in
  `PRIMUS_PROTECTED_HOSTS` unless a config names the exact host in `allow_protected_host`. See
  [`docs/safety.md`](../../docs/safety.md).
- Secrets stay on the server: the skills have the person enter tokens and credentials in Primus's web UI,
  never in chat, configs or scripts.
- The web UI has no login, so the remote skill binds it to localhost and reaches it through an SSH tunnel.

## Helper scripts

- `skills/primus-remote/scripts/remote-run.sh`, `remote-status.sh`, `fetch-results.sh`: start a Primus command
  (or a script) on the server in tmux or nohup, check on it, and copy `results/` back.
- `skills/primus-remote/scripts/primus-docker.sh`: makes the Docker image behave like a local `./primus`.
- `skills/primus-stress/scripts/stress-ladder.sh`: runs on the server and steps a fixed rate up until a step fails.
- `skills/primus-regression/scripts/compare_runs.py`: compares two collection outputs and exits 1 on a regression.
