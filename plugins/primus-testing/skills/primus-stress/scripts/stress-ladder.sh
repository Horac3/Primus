#!/usr/bin/env bash
# Stress ladder: run one endpoint at a fixed request rate, step the rate up, and stop at the first step
# whose assertions fail. The last passing step is the endpoint's sustainable capacity.
# Runs ON THE SERVER, from the Primus work directory (copy it there next to ./primus).
#
#   stress-ladder.sh <request_id> <start_rate> <step> <max_rate> <step_seconds> <max_p95_ms> <max_error_rate>
#   stress-ladder.sh col-123-getOrder 10 10 200 60 500 0.01
#
# Optional environment:
#   CONCURRENCY=200        in-flight cap per step (rate mode drops requests it cannot release: a drop fails the step)
#   EXPECTED_STATUS=200    comma-separated status codes every response must have
#   COOLDOWN=15            seconds of rest between steps, so one step's backlog does not leak into the next
#   DB=primus.db           database file
#   ALLOW_PROTECTED_HOST=  set only when the person has approved loading this protected host
#
# Writes configs/stress-<stamp>-<rate>.json per step and results/stress-<stamp>/ with each run's reports
# and ladder.csv (rate, achieved_rate, dropped, p95_ms, error_rate, outcome). Exit 0 when a failing step
# was found or max_rate passed, 2 on a config error.
set -uo pipefail

if [ $# -ne 7 ]; then
  sed -n '2,20p' "$0" >&2
  exit 2
fi
req="$1"; rate="$2"; step="$3"; max="$4"; secs="$5"; p95="$6"; err="$7"
conc="${CONCURRENCY:-200}"; cool="${COOLDOWN:-15}"; db="${DB:-primus.db}"
stamp="$(date +%Y%m%d-%H%M%S)"
out="results/stress-$stamp"
mkdir -p configs "$out"
echo "rate,achieved_rate,dropped,p95_ms,error_rate,outcome" >"$out/ladder.csv"

num() { sed -n "s/.*\"$1\": *\\([0-9.eE+-]*\\).*/\\1/p" "$2" | head -n1; }
str() { sed -n "s/.*\"$1\": *\"\\([^\"]*\\)\".*/\\1/p" "$2" | head -n1; }

status_json=""
if [ -n "${EXPECTED_STATUS:-}" ]; then status_json="\"expected_status\": [${EXPECTED_STATUS}], "; fi
allow_json=""
if [ -n "${ALLOW_PROTECTED_HOST:-}" ]; then allow_json=", \"allow_protected_host\": \"${ALLOW_PROTECTED_HOST}\""; fi

last_pass=""
while awk "BEGIN{exit !($rate <= $max)}"; do
  cfg="configs/stress-$stamp-$rate.json"
  cat >"$cfg" <<JSON
{
  "id": "cfg-stress-$stamp-$rate",
  "request_id": "$req",
  "mode": "rate",
  "target_rate": $rate,
  "concurrency": $conc,
  "duration": ${secs}000000000,
  "assertions": { ${status_json}"max_p95_millis": $p95, "max_error_rate": $err }$allow_json
}
JSON
  echo "step: $rate req/s for ${secs}s" >&2
  ./primus run "$cfg" --db "$db" --report-dir "$out" >"$out/step-$rate.json" 2>"$out/step-$rate.log"
  code=$?
  if [ $code -eq 2 ]; then
    echo "config error at $rate req/s:" >&2; cat "$out/step-$rate.log" >&2
    exit 2
  fi
  f="$out/step-$rate.json"
  echo "$rate,$(num achieved_rate "$f"),$(num dropped "$f" | grep . || echo 0),$(num p95_millis "$f"),$(num error_rate "$f"),$(str outcome "$f")" >>"$out/ladder.csv"
  if [ $code -ne 0 ]; then
    echo "first failing step: $rate req/s ($(str outcome "$f"))" >&2
    break
  fi
  last_pass="$rate"
  rate="$(awk "BEGIN{print $rate + $step}")"
  sleep "$cool"
done

if [ -n "$last_pass" ]; then
  echo "capacity: last passing step $last_pass req/s (see $out/ladder.csv)"
else
  echo "capacity: even the first step ($2 req/s) failed (see $out/ladder.csv)"
fi
exit 0
