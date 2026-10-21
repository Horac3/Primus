#!/usr/bin/env bash
# claude-kit PreToolUse(Bash) guard: blocks commands that delete or discard work, or read secrets.
# Defence in depth with the deny list in .claude/settings.json (which misses compound commands).
# This guards against accidents, not adversaries.

# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

cmd="$(json_get .tool_input.command)"
[ -z "$cmd" ] && cmd="$HOOK_INPUT"   # could not parse: scan the raw input instead

# Remove quoted strings and heredoc bodies, so commit messages and file contents
# that merely mention "rm" or "clean" do not trigger the guard.
strip_quotes() {
  local s="$1" out="" c q="" i n=${#1}
  for ((i = 0; i < n; i++)); do
    c="${s:i:1}"
    if [ -n "$q" ]; then
      if [ "$q" = '"' ] && [ "$c" = '\' ]; then i=$((i + 1)); continue; fi
      [ "$c" = "$q" ] && { q=""; out+=" "; }
      continue
    fi
    case "$c" in
      "'" | '"') q="$c" ;;
      *) out+="$c" ;;
    esac
  done
  printf '%s' "$out"
}
strip_heredocs() {
  # drop lines between "<<WORD" / "<<'WORD'" / "<<-WORD" and the terminator line WORD
  awk '
    tag != "" { t=$0; sub(/^[ \t]+/, "", t); if (t == tag) tag=""; next }
    { print }
    match($0, /<<-?[ \t]*["'"'"']?[A-Za-z_][A-Za-z0-9_]*/) {
      tag=substr($0, RSTART, RLENGTH); sub(/^<<-?[ \t]*["'"'"']?/, "", tag)
    }'
}

s="$(printf '%s\n' "$cmd" | strip_heredocs)"
s="$(strip_quotes "$s")"
s="$(printf '%s' "$s" | tr '\n' ';')"

# Commands that execute a string (bash -c "...", eval) are checked unstripped too.
EXEC_RE="(^|[;&|[:space:]])(bash|sh|zsh|eval)([[:space:]]+-[a-z]*c)?[[:space:]]+[\"']"
if [[ "$cmd" =~ $EXEC_RE ]]; then
  s="$s;$(printf '%s' "$cmd" | tr "\n\"'" ';  ')"
fi

B='(^|[;&|({`[:space:]]|\$\()'
GIT="${B}git([[:space:]]+-[^[:space:]]+([[:space:]]+[^-;&|[:space:]][^;&|[:space:]]*)?)*[[:space:]]+"
E='([[:space:];&|)]|$)'

check() { # check <regex> <reason>
  if [[ "$s" =~ $1 ]]; then block "$2 (matched: '${BASH_REMATCH[0]# }')"; fi
}

check "${B}(sudo[[:space:]]+)?(rm|rmdir|unlink|shred)${E}"          "deleting files is never done by Claude"
check "${GIT}rm${E}"                                                 "git rm deletes files"
check "${GIT}clean${E}"                                              "git clean deletes untracked work"
check "${GIT}reset[^;&|]*--hard"                                     "git reset --hard discards work"
check "${GIT}checkout[^;&|]*[[:space:]](--|-f|--force|\.)${E}"       "git checkout of paths/force discards work"
check "${GIT}restore${E}"                                            "git restore discards work"
check "${GIT}switch[^;&|]*[[:space:]](-f|--force|--discard-changes)${E}" "forced switch discards work"
check "${GIT}push[^;&|]*[[:space:]](-f|--force|--force-with-lease|--mirror|-d|--delete|--prune)([[:space:]=;&|]|$)" "force/delete pushes rewrite or remove remote history"
check "${GIT}push[^;&|]*[[:space:]]\+[^[:space:]]"                   "force-push refspec"
check "${GIT}push[^;&|]*[[:space:]]:[^[:space:]]"                    "push refspec that deletes a remote branch"
check "${GIT}branch[^;&|]*[[:space:]](-d|-D|--delete)${E}"           "deleting branches"
check "${GIT}tag[^;&|]*[[:space:]](-d|--delete)${E}"                 "deleting tags"
check "${GIT}stash[[:space:]]+(drop|clear)${E}"                      "dropping stashes"
check "${GIT}worktree[[:space:]]+(remove|prune)${E}"                 "removing worktrees"
check "${GIT}(filter-branch|filter-repo|update-ref[[:space:]]+-d)"   "rewriting or deleting git history"
check "${B}find[[:space:]][^;&|]*(-delete|-exec(dir)?[[:space:]]+(rm|rmdir|unlink|shred))" "find -delete / -exec rm"
check "${B}xargs([[:space:]]+[^;&|]*)?[[:space:]](rm|rmdir|unlink|shred)${E}" "xargs rm"
check "${B}(truncate|mkfs(\.[a-z0-9]+)?|wipefs)${E}"                 "destroying file contents"
check "${B}dd[[:space:]][^;&|]*of="                                  "dd writes raw data"
check "${B}mv[[:space:]][^;&|]*[[:space:]]/dev/null${E}"             "moving files to /dev/null deletes them"
check "${B}docker([[:space:]]+[^;&|]*)?[[:space:]](system|volume|image|container|network|builder)[[:space:]]+(prune|rm)${E}" "docker prune/rm deletes data"
check "${B}docker[[:space:]]+(rm|rmi)${E}"                           "docker rm/rmi"
check "${B}docker([[:space:]]+[^;&|]*)?[[:space:]]compose[^;&|]*[[:space:]]down[^;&|]*[[:space:]](-v|--volumes|--rmi)${E}" "compose down -v deletes volumes"
check "${B}docker-compose[^;&|]*[[:space:]]down[^;&|]*[[:space:]](-v|--volumes|--rmi)${E}" "compose down -v deletes volumes"

# Secrets: any command touching a .env file (examples/templates are fine). Checked unstripped.
envscan="$(printf '%s' "$cmd" | sed -E 's/\.env\.(example|sample|template|dist|defaults)//g')"
if [[ "$envscan" =~ (^|[^[:alnum:]_.-])\.env(\.[[:alnum:]_-]+)?([^[:alnum:]_.-]|$) ]]; then
  block "commands that touch .env files could expose secrets"
fi

exit 0
