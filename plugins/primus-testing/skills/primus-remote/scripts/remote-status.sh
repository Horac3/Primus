#!/usr/bin/env bash
# Show whether a remote Primus run is still going, the end of its log, and its exit code once done.
#   remote-status.sh <ssh-host> <run-name>
set -euo pipefail
if [ $# -ne 2 ]; then echo "usage: $0 <ssh-host> <run-name>" >&2; exit 2; fi
host="$1"; name="$2"
home="${PRIMUS_HOME:-~/primus-tests}"
ssh "$host" "cd $home && if [ -f results/${name}.exit ]; then echo \"finished: exit \$(cat results/${name}.exit) (0 pass, 1 failed, 2 config error)\"; \
else echo 'still running'; fi; echo '--- last log lines'; tail -n 15 results/${name}.log 2>/dev/null || true"
