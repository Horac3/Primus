# Agent instructions pointer

This file never changes. It only points to where the real, evolving
instructions live.

Before starting any task:

1. List the contents of /agent_instructions/ and read every file
   there in full, most recently added or modified first.
2. Follow those instructions exactly for the current task.

Gating rule — do not skip this:

- Do not begin the next phase, task, or instruction file in
  /agent_instructions/ until every acceptance/completion condition
  stated in the current one is fully satisfied: tests passing, build
  succeeding, and any explicit checklist item in that file checked
  off.
- If a new file appears in /agent_instructions/ mid-session, treat
  it as superseding instructions for what comes next — read it
  before continuing, but still finish satisfying the current file's
  conditions first unless the new file explicitly says otherwise.

Standing rules that apply to every phase, regardless of which
phase-specific file is active:
- /agent_instructions/testing.md — TDD (red/green/refactor) and
  Arrange-Act-Assert test structure.
- /agent_instructions/session-logging.md — bob_sessions screenshot
  discipline, required for hackathon submission.
