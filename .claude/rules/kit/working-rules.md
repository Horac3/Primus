# Working rules (claude-kit)

## Safety (enforced by hooks; do not try to work around them)
- NEVER delete, move, or discard work: no `rm`, `git clean`, `git reset --hard`, `git checkout --`,
  `git restore`, force pushes, `find -delete`. If something must go, stop and ask the user to do it.
- Existing tracked files are changed with Edit, never rewritten with Write.
- Do not touch `.env*` (except `.env.example`), lockfiles by hand, `.git/`, `.claude/hooks/`, `.claude/settings*.json`.
- Do not create `AGENTS.md`, root-level copies of apps, or duplicate packages.
- If a hook blocks you, report what you wanted to do and why; do not retry a variant of it.

## Working loop
- Small batches. After each batch: run checks via the `test-runner` subagent
  (`.claude/kit/check.sh all`), then `git status --short` and `git diff --stat`.
- If a tool reports failure, inspect `git diff` before retrying. Tool reports have been wrong before.
- Do not commit unless asked or running `/commit`. Never commit a failing suite (except a labelled red-step `test` commit).
- Ask before adding a dependency. Pin exact versions; commit the lockfile.
- Never log secrets, tokens, or user content; log IDs.

## Tests
- Behaviour changes follow Red → Green → Refactor (details in `/tdd`). Bug fixes start with a reproducing test.
- Never edit or delete an existing test to make it pass. The only allowed change is removing
  over-specification, in its own `test(...)` commit with the reason. Anything else: stop and ask.
- Assert the behaviour under test (specific fields) rather than whole-object equality, unless the shape is the behaviour.
- Formatting-only changes go in their own `style:` commit and touch only files you changed.

## Session state
- State lives in `docs/progress.md`, never in chat history. One roadmap step per conversation.
- Ambiguous requirement → stop and ask. Do not guess on anything hard to reverse.
