#!/usr/bin/env bash
# Copy the results directory (run JSON, Markdown reports, series CSV, logs) back from the server.
#   fetch-results.sh <ssh-host> [local-dir]     (default local dir: ./primus-results/<ssh-host>)
# Copies results only. The database and primus.conf stay on the server.
set -euo pipefail
if [ $# -lt 1 ]; then echo "usage: $0 <ssh-host> [local-dir]" >&2; exit 2; fi
host="$1"; dest="${2:-./primus-results/$1}"
home="${PRIMUS_HOME:-~/primus-tests}"
mkdir -p "$dest"
if command -v rsync >/dev/null 2>&1 && ssh "$host" 'command -v rsync >/dev/null'; then
  rsync -az "$host:$home/results/" "$dest/"
else
  scp -rq "$host:$home/results/." "$dest/"
fi
echo "results copied to $dest"
