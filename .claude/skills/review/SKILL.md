---
name: review
description: Review the uncommitted diff with the reviewer subagent on Opus, in a separate context.
argument-hint: "[focus area]"
disable-model-invocation: true
context: fork
agent: reviewer
background: false
---

Review the uncommitted changes in this repository.

Extra focus requested by the user (may be empty): $ARGUMENTS

Changed files right now:
```!
git status --short 2>/dev/null | head -50
git diff --stat 2>/dev/null | tail -20
```
