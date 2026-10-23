#!/usr/bin/env bash
# claude-kit SessionStart hook: injects a few lines of state so a fresh or compacted session
# knows where it is without reading files. Kept deliberately small: it is paid for every session.

# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

source_="$(json_get .source)"
root="$(project_dir)"
progress="$root/docs/progress.md"

branch="$(git -C "$root" branch --show-current 2>/dev/null)"
dirty="$(git -C "$root" status --porcelain 2>/dev/null | wc -l | tr -d ' ')"

echo "[claude-kit] branch: ${branch:-none} · uncommitted files: ${dirty:-0}"

if [ -f "$progress" ]; then
  section() { # print the body of "## <name>" up to the next "## "
    awk -v h="## $1" '$0 == h {on=1; next} /^## / {on=0} on && NF' "$progress"
  }
  step="$(section 'Current step' | head -n 3)"
  next_action="$(section 'Handoff notes' | grep -i -m1 'next action')"
  [ -n "$step" ] && printf '%s\n' "$step" | sed 's/^/[claude-kit] /'
  [ -n "$next_action" ] && printf '[claude-kit] %s\n' "$(printf '%s' "$next_action" | sed 's/^[-* ]*//')"
else
  echo "[claude-kit] no docs/progress.md yet: run /kickoff to set the project up."
fi

stale="$(bash "$root/.claude/kit/context.sh" stale 2>/dev/null)"
[ -n "$stale" ] && echo "[claude-kit] $stale"

case "$source_" in
  compact)
    echo "[claude-kit] Context was just compacted. Re-read only what the current step needs; if the handoff notes are stale, run /handoff before continuing." ;;
  startup | clear)
    echo "[claude-kit] Start a roadmap step with /next." ;;
esac
exit 0
