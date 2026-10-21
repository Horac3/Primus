#!/usr/bin/env bash
# claude-kit hook helpers. Sourced by the hook scripts; not a hook itself.
# Works with jq, python3, or node, and falls back to a crude regex so hooks never depend on one tool.

# Read the hook's JSON input once.
HOOK_INPUT="$(cat)"

# json_get <dotted.path>  -> prints the string value (empty if missing)
json_get() {
  local path="$1"
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$HOOK_INPUT" | jq -r "$path // empty" 2>/dev/null
    return
  fi
  local py=""
  command -v python3 >/dev/null 2>&1 && py=python3
  [ -z "$py" ] && command -v python >/dev/null 2>&1 && py=python
  if [ -n "$py" ]; then
    printf '%s' "$HOOK_INPUT" | "$py" -c '
import sys, json
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for k in sys.argv[1].lstrip(".").split("."):
    d = d.get(k) if isinstance(d, dict) else None
if d is not None:
    sys.stdout.write(d if isinstance(d, str) else json.dumps(d))
' "$path" 2>/dev/null
    return
  fi
  if command -v node >/dev/null 2>&1; then
    printf '%s' "$HOOK_INPUT" | node -e '
let s="";process.stdin.on("data",c=>s+=c).on("end",()=>{try{let d=JSON.parse(s);
for(const k of process.argv[1].replace(/^\./,"").split(".")){d=d&&typeof d==="object"?d[k]:undefined}
if(d!==undefined&&d!==null)process.stdout.write(typeof d==="string"?d:JSON.stringify(d))}catch(e){}})' "$path" 2>/dev/null
    return
  fi
  # Last resort: first "key": "value" for the last path segment (escapes left as-is).
  local key="${path##*.}"
  printf '%s' "$HOOK_INPUT" | tr '\n' ' ' |
    sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\(\([^\"\\\\]\|\\\\.\)*\)\".*/\1/p" | head -n 1
}

# project_dir -> the project root
project_dir() {
  if [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then printf '%s' "$CLAUDE_PROJECT_DIR"; return; fi
  git rev-parse --show-toplevel 2>/dev/null || pwd
}

# block <message>  -> tell Claude why, and stop the tool call
block() {
  printf 'Blocked by claude-kit: %s\n' "$1" >&2
  printf 'Do not retry a variant of this. Tell the user what you wanted to do and why; they can run it themselves.\n' >&2
  exit 2
}
