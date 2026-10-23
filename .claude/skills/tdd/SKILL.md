---
name: tdd
description: Implement behaviours or requirement IDs with a strict Red → Green → Refactor cycle.
argument-hint: "[requirement IDs or behaviour]"
disable-model-invocation: true
model: sonnet
effort: medium
---

Implement $ARGUMENTS test-first. For each behaviour, one at a time:

1. **Red.** Write one small test named after the behaviour; reference the requirement ID in the test
   name or a comment (e.g. `FR-RET-03`). Run just that test via the `test-runner` subagent
   (`check.sh test-one "<name>"`). Show that it fails **for the expected reason** (assertion, not a
   compile error in unrelated code). A test that passes immediately is not Red: rethink it.
2. **Green.** Write the minimum code to pass. Run the full suite via `test-runner`. All green.
3. **Refactor.** Improve names, structure, duplication with all tests green. No new behaviour.
   Run the full suite again.

Rules:
- One behaviour per cycle; keep cycles small. Edge cases are separate behaviours.
- Never edit or delete an existing test to make it pass. If a test looks wrong, stop and explain.
- Domain logic is tested as pure functions with in-memory fakes; never mock domain types.
- Scaffolding/config with no behaviour may skip Red but must leave a passing smoke test.
- Ambiguous requirement: stop and ask before writing the test.

Finish with a table: behaviour · test name · requirement ID · status, and the commits you propose
(`test(scope): …` then `feat(scope): …` then `refactor(scope): …`, or one `feat` per behaviour).
