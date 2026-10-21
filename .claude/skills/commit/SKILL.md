---
name: commit
description: Group the current changes into Conventional Commits and commit them after the user confirms.
argument-hint: "[scope or note]"
disable-model-invocation: true
effort: low
---

Commit the current work. Note from the user (may be empty): $ARGUMENTS

```!
git status --short 2>/dev/null | head -60
git diff --stat 2>/dev/null | tail -30
git diff --staged --stat 2>/dev/null | tail -30
git log --oneline -5 2>/dev/null
```

1. Make sure the suite is green (ask `test-runner` unless it passed after the last edit). Never commit a
   failing suite, except a red-step commit explicitly labelled `test(scope): … (red)`.
2. Group changes into logical commits: tests+behaviour per behaviour, `refactor` separate,
   `style` (formatting only) separate, `docs` separate. Never mix formatting into behaviour commits.
3. Show the plan as a list: commit message → files. Then run `git add <files>` and `git commit` for each;
   the permission prompt is the user's confirmation. Never `git add -A` or `git add .` blindly.

**Conventional Commits:** `<type>(<scope>): <summary>` — imperative, lowercase, no trailing period,
≤ 72 characters.
- Types: `feat` behaviour · `fix` bug · `test` tests only · `refactor` no behaviour change · `docs` ·
  `chore` tooling/config/deps · `build` Docker/build system · `ci` · `perf` · `style` formatting only.
- Scope: the package, app, or module touched (see CLAUDE.md for this project's scopes).
- Body (optional): why, plus `Refs: <requirement IDs>`.
- Breaking change: `feat(scope)!: …` and a `BREAKING CHANGE:` footer.

Do not push.
