#!/usr/bin/env bash
# claude-kit design pack: installs a curated set of third-party frontend design skills and MCP servers
# into THIS project. Nothing is vendored into the kit; everything comes from its upstream source.
#
#   .claude/kit/design-pack.sh                     show the plan for the core pack (nothing is installed)
#   .claude/kit/design-pack.sh core motion --apply install the named packs
#   .claude/kit/design-pack.sh --list              describe every pack
#
# Packs:
#   core      impeccable (taste + critique/audit/polish + DESIGN.md generator) and Vercel web-design-guidelines
#   motion    Emil Kowalski's animation/design-engineering skills (a focused subset)
#   taste     Leonxlnx taste-skill (design-taste-frontend). Overlaps with impeccable: pick one as the main voice
#   shadcn    shadcn MCP server: browse and install registry components (only for shadcn/ui projects)
#   devtools  Chrome DevTools MCP: live browser, console, network, performance traces
#
# Run it from a terminal (or with `!` in Claude Code), not by asking Claude: third-party installers may
# change .claude/settings.json and add hooks, which the kit's guards deliberately stop Claude from doing.

set -u
here="$(cd "$(dirname "$0")" && pwd)"
root="${CLAUDE_PROJECT_DIR:-$(cd "$here/../.." && pwd)}"
cd "$root" || exit 1

apply=0 list=0
declare -a packs=()
for a in "$@"; do
  case "$a" in
    --apply) apply=1 ;;
    --list) list=1 ;;
    core | motion | taste | shadcn | devtools) packs+=("$a") ;;
    all) packs=(core motion shadcn devtools) ;;
    -h | --help) sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $a (try --list)" >&2; exit 2 ;;
  esac
done
[ ${#packs[@]} -eq 0 ] && packs=(core)

dlx="npx"
if [ -f pnpm-lock.yaml ] || [ -f pnpm-workspace.yaml ]; then dlx="pnpm dlx"; fi
[ -f bun.lock ] || [ -f bun.lockb ] && dlx="bunx"
SKILLS="npx -y skills@latest add"
FLAGS="-a claude-code -y --copy"

# Empty arrays are expanded as ${a[@]+"${a[@]}"}: plain "${a[@]}" is an unbound-variable error under set -u in bash < 4.4.
declare -a CMDS=() REPOS=() MANUAL=() NOTES=()
add() { CMDS+=("$1"); REPOS+=("${2:-}"); }   # add <command> [owner/repo of the skills it copies | mcp:<name>]
manual() { MANUAL+=("$1"); }
note() { NOTES+=("$1"); }

# --- licence notices --------------------------------------------------------------------------------
# `skills add --copy` and `impeccable install` copy skill folders without the upstream LICENSE. Committing
# them would redistribute MIT/Apache files without their notices, so each new skill folder gets its upstream
# LICENSE (and NOTICE), and THIRD_PARTY_NOTICES.md lists them. The kit owns only the marked block there.
# Plain bash 3.2: no associative arrays; entries are "key<TAB>line" records merged with awk.
NOTICES="THIRD_PARTY_NOTICES.md"
START="<!-- claude-kit:third-party:start -->"
END="<!-- claude-kit:third-party:end -->"

fetch_upstream() { # fetch_upstream <owner/repo> <file> <dest>; tests set KIT_LICENSE_FIXTURES=<dir>/<owner>__<repo>/<file>
  if [ -n "${KIT_LICENSE_FIXTURES:-}" ]; then
    [ -f "$KIT_LICENSE_FIXTURES/${1%%/*}__${1#*/}/$2" ] && cp "$KIT_LICENSE_FIXTURES/${1%%/*}__${1#*/}/$2" "$3"
    return
  fi
  command -v curl >/dev/null 2>&1 && curl -fsSL --max-time 20 "https://raw.githubusercontent.com/$1/HEAD/$2" -o "$3" 2>/dev/null
}

spdx_of() { # spdx_of <licence text file> <owner/repo>
  if grep -q 'Apache License' "$1" && grep -q 'Version 2\.0' "$1"; then echo "Apache-2.0"
  elif grep -q 'Mozilla Public License' "$1" && grep -q '2\.0' "$1"; then echo "MPL-2.0"
  elif grep -q '^[[:space:]]*MIT License' "$1" || grep -q 'Permission is hereby granted, free of charge' "$1"; then echo "MIT"
  elif grep -q 'Permission to use, copy, modify, and/or distribute' "$1"; then echo "ISC"
  else echo "unknown — check https://github.com/$2"; fi
}

copyright_of() { # first real "Copyright …" line; skips Apache's "Copyright [yyyy] [name …]" template
  grep -E '^[[:space:]]*Copyright' "$1" | grep -v -E '\[yyyy\]|\{yyyy\}' | head -1 | sed 's/^[[:space:]]*//'
}

readme_licence() { # readme_licence <owner/repo>: SPDX id declared under "## License" when upstream has no LICENSE file
  local tmp id; tmp="$(mktemp)"
  if fetch_upstream "$1" README.md "$tmp"; then
    id="$(awk '/^##+[[:space:]]+Licen[cs]e/ { f=1; next } f && /^#/ { exit } f && NF { print $1; exit }' "$tmp")"
  fi
  rm -f "$tmp"
  case "${id:-}" in MIT | Apache-2.0 | MPL-2.0 | ISC | BSD-2-Clause | BSD-3-Clause) echo "$id" ;; *) return 1 ;; esac
}

entry() { printf '%s\t%s <!-- %s -->\n' "$1" "$2" "$1" >>"$ENTRIES"; }   # entry <key> <markdown line>

fetch_licence() { # fetch_licence <owner/repo> <dest>: writes dest only if it does not exist; 0 if dest exists afterwards
  local tmp f
  [ -f "$2" ] && return 0   # already there (e.g. the installer brought one): never overwrite it
  tmp="$(mktemp)"
  for f in LICENSE LICENSE.md LICENSE.txt; do
    if fetch_upstream "$1" "$f" "$tmp" && [ -s "$tmp" ]; then cp "$tmp" "$2"; break; fi
  done
  rm -f "$tmp"
  [ -f "$2" ]
}

license_skill() { # license_skill <skill> <owner/repo>; returns 1 if no licence could be found
  local name="$1" repo="$2" dir=".claude/skills/$1" url="https://github.com/$2" tmp got="" spdx cr
  fetch_licence "$repo" "$dir/LICENSE" && got="$dir/LICENSE"
  if [ -z "$got" ]; then
    if spdx="$(readme_licence "$repo")"; then
      echo "  note: $repo has no LICENSE file; its README declares $spdx. Recorded in $NOTICES, no LICENSE written."
      entry "skill:$name" "- \`$name\` (\`$dir/\`): $url, $spdx (declared in the upstream README; upstream ships no LICENSE file)"
      return 0
    fi
    echo "  WARNING: could not fetch the licence for $name from $repo. Add $dir/LICENSE by hand." >&2
    entry "skill:$name" "- \`$name\` (\`$dir/\`): $url, licence not fetched — add manually"
    return 1
  fi
  spdx="$(spdx_of "$got" "$repo")"
  if [ "$spdx" = "Apache-2.0" ] && [ ! -e "$dir/NOTICE" ]; then
    tmp="$(mktemp)"
    if fetch_upstream "$repo" NOTICE "$tmp" && [ -s "$tmp" ]; then cp "$tmp" "$dir/NOTICE"; fi
    rm -f "$tmp"
  fi
  cr="$(copyright_of "$got")"
  if [ -z "$cr" ] && [ -f "$dir/NOTICE" ]; then cr="$(copyright_of "$dir/NOTICE")"; fi
  entry "skill:$name" "- \`$name\` (\`$dir/\`): $url, $spdx${cr:+, $cr}"
}

license_agent() { # license_agent <agent> <owner/repo> [licence copy from the same command]; 1 if none found
  local name="$1" repo="$2" lic="${3:-}" file=".claude/agents/$1.md" url="https://github.com/$2" spdx cr
  # Agent files are single .md files, so the licence text sits beside them once per upstream
  # (not .md, so Claude Code does not load it as an agent), unless a skill from the same install has a copy.
  if [ -z "$lic" ]; then
    lic=".claude/agents/LICENSE-${repo%%/*}-${repo#*/}"
    if ! fetch_licence "$repo" "$lic"; then
      if spdx="$(readme_licence "$repo")"; then
        entry "agent:$name" "- \`$name\` agent (\`$file\`): $url, $spdx (declared in the upstream README; upstream ships no LICENSE file)"
        return 0
      fi
      echo "  WARNING: could not fetch the licence for agent $name from $repo. Add it by hand." >&2
      entry "agent:$name" "- \`$name\` agent (\`$file\`): $url, licence not fetched — add manually"
      return 1
    fi
  fi
  spdx="$(spdx_of "$lic" "$repo")"; cr="$(copyright_of "$lic")"
  entry "agent:$name" "- \`$name\` agent (\`$file\`): $url, $spdx${cr:+, $cr}; licence text in \`$lic\`"
}

runtime_entry() { # runtime_entry mcp:<name>
  case "$1" in
    mcp:shadcn) entry "$1" "- shadcn MCP server: https://github.com/shadcn-ui/ui, MIT. Runs through npx; nothing is copied." ;;
    mcp:chrome-devtools) entry "$1" "- Chrome DevTools MCP: https://github.com/ChromeDevTools/chrome-devtools-mcp, Apache-2.0. Runs through npx; nothing is copied." ;;
  esac
}

existing_entries() { # key<TAB>line records from the kit block of an existing notices file
  [ -f "$NOTICES" ] || return 0
  awk -v s="$START" -v e="$END" '
    $0 == s { f=1; next } $0 == e { f=0 }
    f && match($0, /<!-- (skill|agent|mcp):[^ ]+ -->$/) { print substr($0, RSTART+5, RLENGTH-9) "\t" substr($0, 1, RSTART+RLENGTH-1) }' "$NOTICES"
}

merged() { # merged <key prefix>: lines for that prefix, one per key, first-seen order, last write wins
  awk -F'\t' -v p="$1" 'index($1, p) == 1 { if (!($1 in v)) o[++n]=$1; v[$1]=$2 } END { for (i=1; i<=n; i++) print v[o[i]] }' "$ENTRIES"
}

render_block() {
  local skills agents runtime
  skills="$(merged skill:)"; agents="$(merged agent:)"; runtime="$(merged mcp:)"
  printf '%s\n' "$START"
  printf '## Third-party skills and agents (maintained by `.claude/kit/design-pack.sh`)\n\n'
  printf 'Copied into this repository from their upstream sources by the claude-kit design pack. Each skill folder carries\n'
  printf 'its upstream `LICENSE` (and `NOTICE` where one exists), and each agent names its licence copy; keep them when you\n'
  printf 'commit or move these files.\n\n'
  printf '%s\n\n' "${skills:-- None yet.}"
  if [ -n "$agents" ]; then printf '%s\n\n' "$agents"; fi
  printf '### Used at runtime, not redistributed\n\n%s\n\n' "${runtime:-- None configured.}"
  printf '### Never commit\n\n'
  printf "%s\n" "- Anthropic's \`frontend-design\` plugin is proprietary (Anthropic Commercial Terms). Each user installs it with"
  printf '%s\n' "  \`/plugin install frontend-design@claude-plugins-official\`; never copy its files into this repository."
  printf '%s' "$END"
}

write_notices() { # rewrites only the marked block; everything outside it is kept byte for byte
  local block content=""
  block="$(render_block)"
  if [ ! -f "$NOTICES" ]; then
    printf '# Third-party notices\n\n%s\n' "$block" >"$NOTICES"
    return
  fi
  IFS= read -r -d '' content <"$NOTICES" || true   # read -d '' keeps trailing newlines, unlike $(cat)
  case "$content" in
    *"$START"*"$END"*) printf '%s%s%s' "${content%%"$START"*}" "$block" "${content#*"$END"}" >"$NOTICES" ;;
    *)
      if [ -n "$content" ] && [ "${content%$'\n'}" = "$content" ]; then content="$content"$'\n'; fi
      printf '%s\n%s\n' "$content" "$block" >"$NOTICES" ;;
  esac
}

skill_dirs() { # sorted names of the folders under .claude/skills/
  local d
  for d in .claude/skills/*/; do [ -d "$d" ] && basename "$d"; done | LC_ALL=C sort
}

agent_files() { # sorted names (without .md) of the agent files under .claude/agents/
  local f
  for f in .claude/agents/*.md; do [ -f "$f" ] && basename "$f" .md; done | LC_ALL=C sort
}

added() { # added <before> <after>: names in after but not before
  LC_ALL=C comm -13 <(printf '%s\n' "$1") <(printf '%s\n' "$2") | grep -v '^$'
}

for p in "${packs[@]}"; do
  case "$p" in
    core)
      add "npx -y impeccable install --providers=claude --scope=project" pbakaus/impeccable
      add "$SKILLS vercel-labs/agent-skills --skill web-design-guidelines $FLAGS" vercel-labs/agent-skills
      manual "/plugin install frontend-design@claude-plugins-official    (Anthropic's frontend-design skill; run inside Claude Code)"
      note "impeccable adds /impeccable <command> (craft, shape, critique, audit, polish, document, …) and a hook that scans UI edits."
      note "web-design-guidelines fetches Vercel's rule list from GitHub on each run; /ui runs it in a subagent so the rules stay out of your context."
      ;;
    motion)
      add "$SKILLS emilkowalski/skills --skill emil-design-eng review-animations apple-design break-ui $FLAGS" emilkowalski/skills
      note "motion: emil-design-eng (motion craft), review-animations, apple-design (Apple interface and motion principles for the web), break-ui (worst-case data)."
      ;;
    taste)
      add "$SKILLS https://github.com/Leonxlnx/taste-skill --skill design-taste-frontend $FLAGS" Leonxlnx/taste-skill
      note "taste overlaps with impeccable and frontend-design. Several always-on taste skills can give conflicting direction: keep one as the main voice."
      ;;
    shadcn)
      if [ -f components.json ]; then
        add "$dlx shadcn@latest mcp init --client claude" mcp:shadcn
      else
        note "shadcn: skipped, no components.json (this project does not use shadcn/ui)."
      fi
      ;;
    devtools)
      add "claude mcp add chrome-devtools --scope project -- npx -y chrome-devtools-mcp@latest --isolated" mcp:chrome-devtools
      note "devtools: an interactive browser for debugging. For routine visual checks, the kit's ui-checker (shot.mjs) is cheaper."
      ;;
  esac
done

if [ $list -eq 1 ]; then sed -n '9,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0; fi

echo "claude-kit design pack: ${packs[*]}"
echo "Shell commands:"
for c in ${CMDS[@]+"${CMDS[@]}"}; do echo "  $c"; done
if [ ${#MANUAL[@]} -gt 0 ]; then
  echo "Inside Claude Code:"
  for m in "${MANUAL[@]}"; do echo "  $m"; done
fi
for n in ${NOTES[@]+"${NOTES[@]}"}; do echo "  · $n"; done

if [ $apply -eq 0 ]; then
  echo
  echo "Nothing installed. Re-run with --apply to install. Third-party skills run with full agent permissions:"
  echo "read them before use (they land in .claude/skills/ and are listed in skills-lock.json)."
  echo "With --apply, each copied skill also gets its upstream LICENSE (and NOTICE), and $NOTICES lists them."
  exit 0
fi

if git rev-parse --is-inside-work-tree >/dev/null 2>&1 &&
  [ -n "$(git status --porcelain -- .claude .mcp.json "$NOTICES" 2>/dev/null)" ]; then
  echo "Uncommitted changes under .claude/, .mcp.json or $NOTICES. Commit them first so you can review exactly what the installers change." >&2
  exit 1
fi

rc=0 missing=0
ENTRIES="$(mktemp)"; trap 'rm -f "$ENTRIES"' EXIT
existing_entries >"$ENTRIES"
i=0
while [ $i -lt ${#CMDS[@]} ]; do
  c="${CMDS[$i]}" repo="${REPOS[$i]}"; i=$((i + 1))
  before="$(skill_dirs)" abefore="$(agent_files)"
  echo "→ $c"
  bash -c "$c"; st=$?
  if [ $st -ne 0 ]; then echo "  failed (exit $st)"; rc=1; continue; fi
  case "$repo" in
    mcp:*) runtime_entry "$repo" ;;
    */*)
      # Only what this command created; its upstream is known per command, never guessed from names.
      lic=""
      while IFS= read -r s; do
        [ -n "$s" ] || continue
        license_skill "$s" "$repo" || missing=1
        if [ -z "$lic" ] && [ -f ".claude/skills/$s/LICENSE" ]; then lic=".claude/skills/$s/LICENSE"; fi
      done <<EOF
$(added "$before" "$(skill_dirs)")
EOF
      while IFS= read -r a; do
        [ -n "$a" ] || continue
        license_agent "$a" "$repo" "$lic" || missing=1
      done <<EOF
$(added "$abefore" "$(agent_files)")
EOF
      ;;
  esac
done
write_notices
if [ $missing -eq 1 ]; then
  echo "Some licences could not be fetched; $NOTICES marks them \"licence not fetched — add manually\"." >&2
  rc=1
fi
echo
echo "Review what changed, then commit it all together: the skills, their LICENSE and NOTICE files, $NOTICES,"
echo "and skills-lock.json (which pins the versions):"
git status --short -- .claude .mcp.json skills-lock.json "$NOTICES" 2>/dev/null | sed 's/^/  /'
[ ${#MANUAL[@]} -gt 0 ] && { echo "Then, inside Claude Code:"; for m in "${MANUAL[@]}"; do echo "  $m"; done; }
exit $rc
