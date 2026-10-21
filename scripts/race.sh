#!/usr/bin/env bash
# Runs the test suite with the race detector inside the golang image.
# The default golang image ships gcc, which -race needs. If your image variant
# lacks it, add: apt-get update -qq && apt-get install -y -qq gcc &&
# MSYS_NO_PATHCONV stops Git Bash on Windows from mangling the mount path.
set -euo pipefail

MSYS_NO_PATHCONV=1 docker run --rm \
  -v "$(pwd)":/src \
  -v primus-gomod:/go/pkg/mod \
  -w /src \
  golang:1.25 \
  go test ./... -race "$@"
