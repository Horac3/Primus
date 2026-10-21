#!/usr/bin/env bash
# claude-kit check runner: one entry point for typecheck / lint / test across Go and TypeScript.
# Prints a one-line verdict per target and only the lines that explain failures; the full log goes
# to .claude/kit/logs/<target>.log. This is what keeps test output from flooding the context.
#
# Usage:
#   .claude/kit/check.sh all                 typecheck, lint, test (stops listing at nothing; runs all)
#   .claude/kit/check.sh typecheck|lint|test|format
#   .claude/kit/check.sh test-one "<pattern>"
#   .claude/kit/check.sh --list              show the configured commands
#
# Commands live in .claude/kit/commands.env (project-owned; edit it freely).

set -u
here="$(cd "$(dirname "$0")" && pwd)"
root="${CLAUDE_PROJECT_DIR:-$(cd "$here/../.." && pwd)}"
cd "$root" || exit 1

conf="$here/commands.env"
if [ ! -f "$conf" ]; then
  echo "check.sh: missing $conf (run the claude-kit installer or create it)" >&2
  exit 1
fi
CHECK_TYPECHECK="" CHECK_LINT="" CHECK_TEST="" CHECK_TEST_ONE="" CHECK_FORMAT=""
CHECK_MAX_LINES=60
# shellcheck disable=SC1090
. "$conf"

logs="$here/logs"
mkdir -p "$logs"
export NO_COLOR=1 FORCE_COLOR=0 CI=1 TERM=dumb

# Lines that usually explain a failure, for Go, Vitest/Jest, tsc, eslint, golangci-lint, pnpm.
FAIL_RE='(--- FAIL|^FAIL|FAIL |panic:|^ok  .*FAIL|[Ee]rror|ERR!|ERR_|✗|×|❯ .*\.(ts|tsx|js)|AssertionError|[Ee]xpected|[Rr]eceived|\.go:[0-9]+:[0-9]*|\.(ts|tsx|mts|cts|js):[0-9]+(:[0-9]+)?|error TS[0-9]+|warning|Warn:|undefined:|cannot use|declared and not used|not enough arguments|Test Files .*failed|Tests .*failed)'
NOISE_RE='(^=== (RUN|PAUSE|CONT)|^--- PASS|^PASS$|^ok  |^\?   .*\[no test files\]|^ *✓|^ *√|^$)'

# Collapse runs of lines that differ only in numbers (log loops, retries, progress) into one line.
collapse() {
  awk '
    { k = $0; gsub(/[0-9]+/, "#", k) }
    NR > 1 && k == pk { c++; next }
    { if (c > 0) print "  … (+" c " similar lines)"; print; pk = k; c = 0 }
    END { if (c > 0) print "  … (+" c " similar lines)" }'
}

# A zero exit that ran nothing must not read as PASS: the workspace runner matched no package, or no package
# has the script. pnpm/npm/bun script runners always print what they run, so one that prints nothing (or only
# pnpm's "Scope: …" line) ran nothing. Silence alone is not enough: go vet and gofmt -l are silent on success.
NOTHING_RE='No projects matched|None of the selected packages has a|No packages matched'
RUNNER_RE='(^|[^[:alnum:]_-])(pnpm|npm|bun)[[:space:]]([^;&|]*[[:space:]])?run([[:space:]]|$)'
nothing_ran() { # nothing_ran <command> <log>
  grep -qE "$NOTHING_RE" "$2" && return 0
  printf '%s' "$1" | grep -qE "$RUNNER_RE" || return 1
  ! grep -vE '^Scope: [0-9]+ of [0-9]+ workspace projects' "$2" | grep -q '[^[:space:]]'
}

summary_line() { # best-effort count line from a passing run
  grep -E '(Tests? +[0-9]+ passed|Test Files +[0-9]+ passed|^ok  |passed|PASS)' "$1" 2>/dev/null |
    grep -vE '^ok  ' | tail -n 1
  local oks; oks="$(grep -cE '^ok  ' "$1" 2>/dev/null)"
  [ "${oks:-0}" -gt 0 ] && echo "$oks Go package(s) ok"
}

run_target() { # run_target <name> <command> [args...]
  local name="$1" cmd="$2"; shift 2
  local log="$logs/$name.log"
  if [ -z "$cmd" ]; then
    echo "SKIP $name (no command configured)"
    return 0
  fi
  local start end status lines
  start=$(date +%s)
  bash -c "$cmd" _ "$@" >"$log" 2>&1
  status=$?
  end=$(date +%s)
  lines=$(wc -l <"$log" | tr -d ' ')
  if [ "$status" -eq 0 ]; then
    if nothing_ran "$cmd" "$log"; then
      echo "SKIP $name (nothing ran)"
      return 0
    fi
    local s; s="$(summary_line "$log" | tr '\n' ' ' | sed 's/  */ /g')"
    echo "PASS $name ($((end - start))s)${s:+ — $s}"
    return 0
  fi
  echo "FAIL $name (exit $status, $((end - start))s) — $cmd"
  local hits n max="$CHECK_MAX_LINES" head_n
  hits="$(grep -vE "$NOISE_RE" "$log" | collapse | grep -E -A 3 "$FAIL_RE" | grep -v '^--$')"
  [ -z "$hits" ] && hits="$(collapse <"$log" | tail -n 30)"
  n=$(printf '%s\n' "$hits" | wc -l | tr -d ' ')
  if [ "$n" -gt "$max" ]; then
    # keep the start (which test failed) and the end (the assertion usually comes last)
    head_n=$((max / 3))
    {
      printf '%s\n' "$hits" | head -n "$head_n"
      echo "… ($((n - max)) lines omitted)"
      printf '%s\n' "$hits" | tail -n $((max - head_n))
    } | sed 's/^/  /'
  else
    printf '%s\n' "$hits" | sed 's/^/  /'
  fi
  echo "  full log: .claude/kit/logs/$name.log ($lines lines)"
  return "$status"
}

target="${1:-all}"
[ $# -gt 0 ] && shift
case "$target" in
  --list)
    grep -E '^CHECK_' "$conf" ;;
  typecheck) run_target typecheck "$CHECK_TYPECHECK" ;;
  lint) run_target lint "$CHECK_LINT" ;;
  test) run_target test "$CHECK_TEST" "$@" ;;
  format) run_target format "$CHECK_FORMAT" ;;
  test-one)
    if [ $# -lt 1 ]; then echo "usage: check.sh test-one <pattern>" >&2; exit 2; fi
    run_target test-one "$CHECK_TEST_ONE" "$@" ;;
  all)
    rc=0
    run_target typecheck "$CHECK_TYPECHECK" || rc=1
    run_target lint "$CHECK_LINT" || rc=1
    run_target test "$CHECK_TEST" || rc=1
    if [ "$rc" -eq 0 ]; then echo "VERDICT: PASS"; else echo "VERDICT: FAIL"; fi
    exit "$rc" ;;
  *)
    echo "usage: check.sh [all|typecheck|lint|test|format|test-one <pattern>|--list]" >&2
    exit 2 ;;
esac
