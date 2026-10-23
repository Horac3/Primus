#!/usr/bin/env bash
# claude-kit context gatherer: cheap, deterministic summaries of a repository, so Claude reads
# a few dozen lines instead of exploring. Used by /kickoff and /remap; safe to run any time.
#
#   context.sh layout              directory tree (tracked files only) with file counts, 2 levels
#   context.sh existing            instruction files, docs, ADRs, API specs already in the repo
#   context.sh history [N]         recent commits, hotspots, active branches, TODO density
#   context.sh drift               what changed since docs/architecture.md was last mapped
#   context.sh mode                prints "greenfield" or "existing"

set -u
here="$(cd "$(dirname "$0")" && pwd)"
root="${CLAUDE_PROJECT_DIR:-$(cd "$here/../.." && pwd)}"
cd "$root" || exit 1

in_git() { git rev-parse --is-inside-work-tree >/dev/null 2>&1; }
files() { if in_git; then git ls-files; else find . -type f -not -path './.git/*' | sed 's|^\./||'; fi; }
# Files that are not the kit's own and not docs/config noise
source_files() {
  files | grep -vE '^(\.claude/|docs/|CLAUDE(\.local)?\.md$|\.gitignore$|\.gitattributes$)' |
    grep -vE '(^|/)(node_modules|vendor|dist|build|\.next|coverage)/' |
    grep -vE '\.(md|txt|lock|sum)$|lock\.(json|yaml)$'
}
lines() { wc -l <"$1" 2>/dev/null | tr -d ' '; }
ARCH=docs/architecture.md

mapped_sha() { [ -f "$ARCH" ] && sed -n 's/.*mapped-at: \([0-9a-f]\{7,40\}\).*/\1/p' "$ARCH" | head -n 1; }

cmd_mode() {
  local n; n=$(source_files | wc -l | tr -d ' ')
  if [ "$n" -ge 10 ]; then echo existing; else echo greenfield; fi
}

cmd_layout() {
  echo "## Layout (tracked files; dir: count)"
  files | grep -vE '(^|/)(node_modules|vendor|dist|build)/' | awk -F/ '
    NF == 1 { top["."]++; next }
    { top[$1]++; if (NF > 2) sub2[$1 "/" $2]++ }
    END {
      for (d in top) print d "\t" top[d]
      for (s in sub2) print s "\t" sub2[s]
    }' | sort | awk -F'\t' '{ n = split($1, p, "/"); printf "%s%s/  %s\n", (n > 1 ? "  " : ""), $1, $2 }' | head -n 80
  echo "source files: $(source_files | wc -l | tr -d ' ') · mode: $(cmd_mode)"
}

cmd_existing() {
  echo "## Instruction files for AI tools (rules worth importing)"
  local f found=0
  for f in CLAUDE.md CLAUDE.local.md AGENTS.md GEMINI.md CONVENTIONS.md .cursorrules .windsurfrules .clinerules \
    .github/copilot-instructions.md; do
    [ -f "$f" ] && { echo "- $f ($(lines "$f") lines)"; found=1; }
  done
  for f in .cursor/rules/* .clinerules/* .github/instructions/*; do
    [ -f "$f" ] && { echo "- $f ($(lines "$f") lines)"; found=1; }
  done
  if [ -f CLAUDE.md ] && grep -q 'TODO(kickoff)' CLAUDE.md; then echo "  (CLAUDE.md is the kit template)"; fi
  [ -f CLAUDE.md.kit-new ] && echo "  (CLAUDE.md.kit-new present: the project had its own CLAUDE.md before install)"
  [ $found -eq 0 ] && echo "- none"

  echo "## Project docs"
  files | grep -iE '^(readme|contributing|architecture|design|development|hacking|security)[^/]*\.(md|rst|txt)$' |
    while read -r f; do echo "- $f ($(lines "$f") lines)"; done
  files | grep -E '^docs?/.*\.(md|rst)$' | grep -vE '^docs/(progress|decisions|requirements|architecture)\.md$' |
    grep -viE '/(adr|adrs|decisions?)/' | head -n 25 | while read -r f; do echo "- $f ($(lines "$f") lines)"; done

  echo "## Decision records"
  local adrs; adrs="$(files | grep -iE '(^|/)(adr|adrs|decisions?|architecture/decisions)/[^/]+\.md$' | head -n 40)"
  if [ -n "$adrs" ]; then
    printf '%s\n' "$adrs" | while read -r f; do
      printf -- '- %s — %s\n' "$f" "$(grep -m1 -E '^#' "$f" 2>/dev/null | sed 's/^#* *//')"
    done
  else echo "- none"; fi

  echo "## Contracts and schemas"
  files | grep -iE '(openapi|swagger)[^/]*\.(ya?ml|json)$|\.proto$|\.graphqls?$|schema\.prisma$|(^|/)migrations?/' |
    sed -E 's|(/migrations?)/.*|\1/ (dir)|' | sort -u | head -n 20 | sed 's/^/- /'

  echo "## Build, CI, deploy"
  files | grep -E '^(Makefile|Taskfile\.ya?ml|justfile|Dockerfile[^/]*|[^/]*compose[^/]*\.ya?ml|\.github/workflows/[^/]+|\.gitlab-ci\.yml|turbo\.json|nx\.json|go\.work|pnpm-workspace\.yaml)$' |
    head -n 20 | sed 's/^/- /'
}

cmd_history() {
  in_git || { echo "## History: not a git repository"; return 0; }
  local n="${1:-25}"
  echo "## Recent commits (last $n)"
  git log --oneline --no-decorate -n "$n" 2>/dev/null
  echo "## Hotspots: most-changed source files, last 200 commits"
  git log -n 200 --name-only --format= 2>/dev/null | grep -v '^$' |
    grep -vE '(^|/)(node_modules|vendor|dist)/|\.(lock|sum|md)$|lock\.(json|yaml)$|^docs/|^\.claude/' |
    sort | uniq -c | sort -rn | head -n 15 | awk '{printf "- %s (%s commits)\n", $2, $1}'
  echo "## Active branches"
  git for-each-ref --sort=-committerdate --count=8 \
    --format='- %(refname:short) · %(committerdate:relative) · %(subject)' refs/heads 2>/dev/null
  echo "## TODO / FIXME density (top files)"
  git grep -c -E '\b(TODO|FIXME|HACK|XXX)\b' -- ':!docs' ':!.claude' ':!*.md' 2>/dev/null |
    sort -t: -k2 -rn | head -n 10 | awk -F: '{printf "- %s (%s)\n", $1, $2}'
  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    echo "## Open issues (gh)"
    gh issue list --limit 15 --json number,title,labels \
      --jq '.[] | "- #\(.number) \(.title) [\(.labels | map(.name) | join(","))]"' 2>/dev/null || echo "- unavailable"
  fi
}

cmd_drift() {
  if [ ! -f "$ARCH" ]; then echo "drift: no $ARCH yet (run /kickoff)"; return 0; fi
  local sha; sha="$(mapped_sha)"
  if [ -z "$sha" ] || ! git cat-file -e "$sha^{commit}" 2>/dev/null; then
    echo "drift: $ARCH has no valid mapped-at marker; treat the whole map as unverified"
    return 0
  fi
  local commits; commits=$(git rev-list --count "$sha"..HEAD 2>/dev/null)
  echo "## Drift since $sha ($commits commits)"
  echo "### Structural changes (added / deleted / renamed source files, grouped by directory)"
  git diff --name-status -M "$sha" HEAD 2>/dev/null | awk '$1 ~ /^(A|D|R)/' |
    grep -vE '\t(docs/|\.claude/)' | awk -F'\t' '{
      path = ($1 ~ /^R/) ? $3 : $2; n = split(path, p, "/");
      dir = (n > 2) ? p[1] "/" p[2] : (n == 2 ? p[1] : ".");
      k = substr($1, 1, 1); c[dir " " k]++ }
      END { for (x in c) { split(x, y, " "); printf "- %s: %s %s\n", y[1], c[x], (y[2] == "A" ? "added" : y[2] == "D" ? "deleted" : "renamed") } }' |
    sort | head -n 30
  echo "### Dependency manifests changed"
  git diff --stat "$sha" HEAD -- '**/go.mod' 'go.mod' '**/package.json' 'package.json' 'go.work' 'pnpm-workspace.yaml' 2>/dev/null |
    sed 's/^/- /' | head -n 10
  echo "### Paths named in the map that no longer exist"
  grep -oE '`[A-Za-z0-9_.@-]+(/[A-Za-z0-9_.@*-]+)+/?`' "$ARCH" CLAUDE.md 2>/dev/null | cut -d: -f2- | tr -d '`' |
    grep -v '\*' | sort -u | while read -r p; do [ -e "$p" ] || echo "- $p"; done
}

cmd_stale() { # one-line staleness summary for the session hook; silent when fresh
  [ -f "$ARCH" ] || return 0
  local sha; sha="$(mapped_sha)"
  [ -n "$sha" ] && git cat-file -e "$sha^{commit}" 2>/dev/null || return 0
  local commits structural
  commits=$(git rev-list --count "$sha"..HEAD 2>/dev/null)
  structural=$(git diff --name-status "$sha" HEAD 2>/dev/null | awk '$1 ~ /^(A|D|R)/' | grep -cvE '\t(docs/|\.claude/)')
  if [ "${structural:-0}" -ge 15 ] || [ "${commits:-0}" -ge 60 ]; then
    echo "docs/architecture.md is $commits commits and $structural structural changes behind: run /remap when convenient."
  fi
}

case "${1:-}" in
  layout) cmd_layout ;;
  existing) cmd_existing ;;
  history) shift; cmd_history "$@" ;;
  drift) cmd_drift ;;
  stale) cmd_stale ;;
  mode) cmd_mode ;;
  *) echo "usage: context.sh layout|existing|history [N]|drift|stale|mode" >&2; exit 2 ;;
esac
exit 0
