# Session logging discipline

After completing any task in Agent or Plan mode that changes the
codebase, before starting the next task:

1. Confirm the task's tests are green and the build succeeds.
2. Take a screenshot of the task's session summary (the completed
   task view showing what changed and Bobcoin cost).
3. Save it to /bob_sessions/ using the naming pattern
   phase-<n>-<short-slug>.png, e.g. phase-1-domain-types.png.
4. Do not start the next phase's prompt until that screenshot exists.

This is a hackathon submission requirement, not optional cleanup —
treat an unlogged session as an unfinished task.
