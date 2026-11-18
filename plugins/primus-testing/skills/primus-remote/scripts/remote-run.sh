#!/usr/bin/env bash
# Start a Primus command on a remote server so it survives SSH disconnects, then return at once.
#
#   remote-run.sh <ssh-host> <run-name> <primus args...>
#   remote-run.sh <ssh-host> <run-name> --exec <script in $PRIMUS_HOME> <script args...>
#   remote-run.sh perf-box smoke-2026-11-17 run --collection "Orders API" --report-dir results/smoke-2026-11-17
#   remote-run.sh perf-box stress-orders --exec stress-ladder.sh col-123-getOrder 10 10 200 60 500 0.01
#
# On the server it runs, inside $PRIMUS_HOME (default ~/primus-tests):
#   ./primus <args> --db primus.db  >results/<run-name>.json  2>results/<run-name>.log
# or, with --exec, ./<script> <args> >results/<run-name>.out (the script picks its own --db).
# with primus.conf (if present) loaded as environment, and writes the exit code to results/<run-name>.exit.
# Uses tmux when the server has it (session "primus-<run-name>"), otherwise nohup.
# Check on it with remote-status.sh, copy results back with fetch-results.sh.
set -euo pipefail

if [ $# -lt 3 ]; then
  echo "usage: $0 <ssh-host> <run-name> <primus args...>" >&2
  exit 2
fi
host="$1"; name="$2"; shift 2
case "$name" in *[!A-Za-z0-9._-]*) echo "run-name may only hold letters, digits, . _ -" >&2; exit 2 ;; esac

home="${PRIMUS_HOME:-~/primus-tests}"
cmd="./primus"
db=" --db primus.db"
ext="json"
if [ "$1" = "--exec" ]; then
  [ $# -ge 2 ] || { echo "--exec needs a script name" >&2; exit 2; }
  case "$2" in */* | .*) echo "--exec takes a file name inside \$PRIMUS_HOME, not a path" >&2; exit 2 ;; esac
  cmd="./$(printf '%q' "$2")"; db=""; ext="out"; shift 2
fi
args=""
for a in "$@"; do args+=" $(printf '%q' "$a")"; done

# The job: load the conf, run primus, record the exit code. Built here, quoted once for the remote shell.
job="cd $home && mkdir -p results && { [ -f primus.conf ] && set -a && . ./primus.conf && set +a; true; } && ${cmd}${args}${db} >results/${name}.${ext} 2>results/${name}.log; echo \$? >results/${name}.exit"

remote="cd $home && rm -f results/${name}.exit && if command -v tmux >/dev/null 2>&1; then \
tmux new-session -d -s primus-${name} $(printf '%q' "bash -c $(printf '%q' "$job")") && echo 'started in tmux session primus-${name}'; \
else nohup bash -c $(printf '%q' "$job") >/dev/null 2>&1 & echo 'started with nohup (pid '\$!')'; fi"

ssh "$host" "$remote"
