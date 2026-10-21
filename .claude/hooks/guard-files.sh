#!/usr/bin/env bash
# claude-kit PreToolUse(Edit|Write|MultiEdit|NotebookEdit) guard:
# protects secrets, lockfiles, git internals, the guard config itself, and tracked files from full rewrites.

# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

tool="$(json_get .tool_name)"
file="$(json_get .tool_input.file_path)"
[ -z "$file" ] && file="$(json_get .tool_input.notebook_path)"
[ -z "$file" ] && exit 0

file="${file//\\//}"            # Windows separators
base="${file##*/}"
root="$(project_dir)"; root="${root//\\//}"
rel="${file#"$root"/}"

case "$base" in
  .env.example | .env.sample | .env.template | .env.dist | .env.defaults) ;;
  .env | .env.*) block "$rel may hold secrets; ask the user to edit it" ;;
esac

case "/$rel" in
  */.git/*) block "never write inside .git/" ;;
  */.claude/hooks/* | */.claude/settings.json | */.claude/settings.local.json)
    block "$rel is the safety config; propose the change and let the user apply it" ;;
esac

case "$base" in
  pnpm-lock.yaml | package-lock.json | yarn.lock | bun.lock | bun.lockb | go.sum | go.work.sum | Cargo.lock | poetry.lock | uv.lock)
    block "$base is generated; change it only through the package manager" ;;
  AGENTS.md)
    block "this project uses CLAUDE.md; do not create or edit AGENTS.md" ;;
esac

# Write replaces a whole file. Tracked files must be changed with Edit so nothing is silently lost.
if [ "$tool" = "Write" ] && [ -s "$file" ] &&
  git -C "$root" ls-files --error-unmatch -- "$file" >/dev/null 2>&1; then
  block "$rel is tracked by git; change it with Edit instead of rewriting it with Write"
fi

exit 0
