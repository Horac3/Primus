#!/usr/bin/env bash
# claude-kit status line: model · context % (colour-coded) · cost · branch · current step.
# Yellow at 50% context means "finish this cycle"; red at 70% means "/handoff now".

input="$(cat)"

get() { # get <jq path> <fallback key>
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$input" | jq -r "$1 // empty" 2>/dev/null
  else
    printf '%s' "$input" | tr -d '\n' | grep -o "\"$2\"[[:space:]]*:[[:space:]]*\"\{0,1\}[^\",}]*" | head -n 1 |
      sed -E 's/.*:[[:space:]]*"?//'
  fi
}

model="$(get .model.display_name display_name)"
pct="$(get .context_window.used_percentage used_percentage)"
cost="$(get .cost.total_cost_usd total_cost_usd)"
dir="$(get .workspace.project_dir project_dir)"
[ -z "$dir" ] && dir="$(get .workspace.current_dir current_dir)"
[ -z "$dir" ] && dir="$PWD"

branch="$(git -C "$dir" branch --show-current 2>/dev/null)"
[ -n "$branch" ] && [ -n "$(git -C "$dir" status --porcelain 2>/dev/null | head -n 1)" ] && branch="$branch*"

step=""
if [ -f "$dir/docs/progress.md" ]; then
  step="$(awk '$0 == "## Current step" {on=1; next} /^## / {on=0} on && NF {print; exit}' "$dir/docs/progress.md")"
  [ "${#step}" -gt 48 ] && step="${step:0:47}…"
fi

reset=$'\033[0m'; dim=$'\033[2m'
ctx=""
if [ -n "$pct" ]; then
  p="${pct%%.*}"
  if [ "$p" -ge 70 ]; then ctx=$'\033[31m'"ctx ${p}% · /handoff now${reset}"
  elif [ "$p" -ge 50 ]; then ctx=$'\033[33m'"ctx ${p}% · finish cycle${reset}"
  else ctx=$'\033[32m'"ctx ${p}%${reset}"
  fi
fi

out="${model:-?}"
[ -n "$ctx" ] && out="$out · $ctx"
[ -n "$cost" ] && out="$out ${dim}· \$$(printf '%.2f' "$cost" 2>/dev/null || echo "$cost")${reset}"
[ -n "$branch" ] && out="$out ${dim}· ${branch}${reset}"
[ -n "$step" ] && out="$out ${dim}· ${step}${reset}"
printf '%s\n' "$out"
