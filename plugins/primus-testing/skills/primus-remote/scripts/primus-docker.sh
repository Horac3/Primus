#!/usr/bin/env bash
# Wrapper that makes the Primus Docker image behave like a local ./primus binary.
# Copy it to the server as ~/primus-tests/primus (chmod +x) when you use the image instead of a binary.
#   IMAGE defaults to primus:local (built with: docker build -f deploy/Dockerfile -t primus:local .)
# Runs as your own user so files it writes (results/, primus.db) belong to you, with the work
# directory mounted at the same path, and host networking so targets on localhost are reachable.
set -euo pipefail
image="${PRIMUS_IMAGE:-primus:local}"
work="$(cd "$(dirname "$0")" && pwd)"
envfile=()
[ -f "$work/primus.conf" ] && envfile=(--env-file "$work/primus.conf")
exec docker run --rm -i --network host -u "$(id -u):$(id -g)" \
  -v "$work:$work" -w "$work" "${envfile[@]}" "$image" "$@"
