---
name: fix
description: Fix a bug test-first — reproduce it with a failing test, root-cause it, fix minimally, and prove it stays fixed.
argument-hint: "[bug description, error, or issue]"
disable-model-invocation: true
effort: high
---

Fix this bug: $ARGUMENTS

1. **Understand.** Restate the expected vs actual behaviour in two lines. If either is unclear, ask.
2. **Reproduce.** Write the smallest test that reproduces the bug at the lowest layer where it occurs.
   Run it via `test-runner` and show it failing for the reported reason.
3. **Root-cause.** If the cause is not obvious after one look at the failing path, delegate to the
   `debugger` subagent with: the symptom, the failing test name, and the files involved.
4. **Fix minimally.** Change only what the root cause requires. No drive-by refactors or formatting.
5. **Prove.** Run the full suite via `test-runner`. All green.
6. **Look for siblings.** One grep for the same pattern elsewhere. Report any, do not fix them unasked.

Finish with: root cause (one sentence), the test added, files changed, and the proposed commits
(`test(scope): reproduce <bug>` + `fix(scope): <summary>`, or a single `fix` commit).
