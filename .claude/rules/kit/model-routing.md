# Model routing and token economy (claude-kit)

Main session runs `opusplan`: Opus in plan mode, Sonnet once the plan is approved.
Route everything else by delegation, not by switching the main model.

| Work | Who does it | Model · effort |
|---|---|---|
| Plan a step, weigh architecture | main session, plan mode | Opus (opusplan) |
| Implement an approved plan, TDD cycles | main session / `/tdd` | Sonnet · medium |
| Run tests, lint, typecheck | `test-runner` subagent | Haiku · low |
| Find code: "where is X", "how does Y flow" | built-in `Explore` subagent | Haiku |
| Library / API / version lookups | `researcher` subagent | Sonnet · low |
| Root-cause a failure not obvious after one look | `debugger` subagent | Sonnet · high |
| End-of-step review | `reviewer` subagent (`/review`) | Opus · high |
| Irreversible or cross-cutting decision | `/decide` (forked) | Opus · high |

Rules of thumb:
1. **Never switch the main model mid-conversation.** The new model re-reads the whole context
   uncached. Switch only right after `/clear`. If a task needs a bigger model, delegate it.
2. **Verbose output belongs in subagents.** Test logs, search sweeps, doc pages and log files stay
   in the subagent; only its summary enters this context. Never paste full logs here.
3. **Read narrowly.** Grep or Explore first, then read line ranges. Do not re-read a file you just
   edited. Do not read files "for context" that the step does not touch.
4. **`/clear` is free, `/compact` is not.** Finish a cycle, run `/handoff`, then `/clear` + `/next`.
   The status line turns yellow at 50% context and red at 70%: at red, hand off now.
5. **Give subagents tight briefs**: the exact question, the files or IDs involved, and the answer
   format you want. They start with no memory of this conversation.
