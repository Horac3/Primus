---
name: decide
description: Think through an architectural or irreversible decision on Opus in a separate context and return a decision record to approve.
argument-hint: "[the decision to make, with constraints]"
disable-model-invocation: true
context: fork
agent: Plan
model: opus
effort: high
background: false
---

Decide: $ARGUMENTS

Ground the decision in this repository. Read `CLAUDE.md`, the relevant parts of `docs/decisions.md`
(do not contradict an earlier decision without saying so), and only the code the decision touches.

**Match the log's existing entry style.** If `docs/decisions.md` (or a project rule about it) uses another
heading style, e.g. `## <Topic> (step N)`, use that heading and numbering instead of `D-NNN`, and keep the body
fields below. Never mix two styles in one log.

Return a decision record in this shape, under ~400 words:

```
## D-<next number>: <title>
Date: <today>
Status: proposed
Context: <the forces at play, 2–4 lines>
Options:
1. <option> — pros / cons / cost to reverse
2. <option> — pros / cons / cost to reverse
3. <option, if a real one exists>
Decision: <recommended option and the one-line reason>
Consequences: <what changes, what it rules out, what to watch>
Revisit if: <the signal that would make this wrong>
```

After the record, add one line for the main conversation:
`Approve to append this to docs/decisions.md with Status: accepted.`
