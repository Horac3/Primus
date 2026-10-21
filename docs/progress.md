# Progress

State lives here, not in chat history. `/next` reads "Current step" and "Handoff notes";
`/handoff` rewrites them. Markers: `[ ]` todo · `[~]` in progress · `[x]` done.

## Roadmap
- [x] 1. Credential pools: types, storage, login phase, UI, smoke test (`848a90c`..`16afb8c`)
- [x] 2. Fix: a run with zero completed requests never passes (`3bc3962`)
- [x] 3. Fix: cancelled in-flight requests counted separately from errors (`64b7c95`, `6e20275`)
- [x] 4. Fix: flaky `TestProbe_SuccessfulTarget` (`LatencyMs <= 0` at `internal/engine/engine_phase4_test.go:152`).
  Reproduce at `-count=50`, assert `< 0` instead, no sleeps, and look for the same `> 0`-on-a-measured-duration
  pattern elsewhere. Spec: `agent_instructions/fix-verdict-and-flaky-probe.md` (Fix 2). (`d1326cf`)
- [x] 5. CI: add a test job (build, vet, test, then `scripts/race.sh`-equivalent `go test -race`) that runs on pushes
  and pull requests, and make the Docker publish job depend on it. NFR-CI-01.
- [x] 6. Per-field credential masking. FR-CRED-01..04. Spec: `agent_instructions/fix-credential-field-masking.md`.
- [x] 7. Docs and UI follow-ups from the setup check (README auth-probe wording was OK and is excluded):
  - `docs/credential-pools.md`: re-login on 401 is documented as off by default, but nothing says when to turn it on.
    A soak run with short-lived tokens 401s after expiry unless it is on. Add that guidance.
  - Analyzer default: `deploy/docker-compose.yml` sets `PRIMUS_ANALYZER=ollama` unconditionally. With the `ai` profile
    off, `NewAnalyzer` still builds the Ollama analyzer (no reachability check), so insights start labelled
    `ollama`/`qwen2.5:3b`, fail on the unresolvable `ollama` host, then fall back to rules (final label `rules`).
    The natural-language authoring and payload-generation UI stays enabled and returns 502 on use. Decide the default
    (rules unless the profile is on, or say in the UI that Ollama is unreachable). Leave `depends_on` to the user. (`975b829`, `611b4f8`)
- [ ] 8. Auth-probe UI wiring: attempt count, rate and credential fields in the UI. Write FR-PROBE-NN first.
- [ ] 9. Open-loop fixed req/s mode. FR-RATE-01..04. Start the step with `/decide` on the dispatcher design
  (ticker-driven release, in-flight cap, `Dropped` accounting, latency from intended send time).
- [ ] 10. LLM: query run history in natural language. Needs requirements and a spec first.
- [ ] 11. LLM: commit-message assistant ("meta"). Needs requirements and a spec first.
- [ ] 12. Downloadable test reports and insights. Needs scoping.
- [ ] 13. Credential provisioning (sign-up before the run). Later and advanced; reuses the `Extraction` primitive.
  Needs scoping and a `/decide`.
- [ ] 14. Refactor: split `internal/web/web.go` and `internal/engine/engine.go` by concern. Do it as the first
  commit of whichever step next touches each file, not as a standalone big-bang change.

## Current step
Step 8: Auth-probe UI wiring
Requirements: FR-PROBE-NN (to be written first)
Status: not started

## Handoff notes
- Done: step 7 (`975b829` re-login-on-401 guidance in `docs/credential-pools.md`; `611b4f8` compose analyzer
  defaults to rules, `docs/cli.md` updated, D-010). Docs/config only; `docker compose config` verified both values.
- Next action: `/next` for step 8: write FR-PROBE-NN in `docs/requirements.md`, then wire attempt count, rate and
  credential fields into the auth-probe UI.
- Uncommitted: none.
- Decisions: D-010 (recorded).
- Watch out: with no `expected_status`, a 401 counts as a completed request, not an error (`engine.go:562`).
  A worker that adopts a shared token still uses its single re-login (`pool.go:208`).
- Deferred (reviewer, step 6): spec save and masking save each rewrite the whole pool payload; fix via `json_set`.
- `TestRunInsightAsyncLifecycleAndReuse` flaked once under full-suite load.
- Unverified: CI not yet run on GitHub; `scripts/race.sh` never run locally (Docker is now available).
- Lint strips CR before gofmt (`core.autocrlf=true`); CRLF-only gofmt hits on untouched files: leave them.
- Manual steps the user does by hand, never Claude: `git filter-repo`, interactive rebase, compose `depends_on`,
  Docker Hub push, deleting `AGENTS.md`/`.bobignore`/`primus-scaffold-plan.md`/root `web/`, repo-wide `eol` rule.

## Log
- 2026-10-21 claude-kit 1.4.0 installed into Primus on `ft/dev`; roadmap rebuilt from the repo state at `6e20275`
- 2026-10-21 setup verified: `commands.env` created, `check.sh all` PASS, race run blocked (Docker down); roadmap reset to steps 4–14
- 2026-10-21 step 4 done (`d1326cf`)
- 2026-10-21 step 5 done (`09f025e`)
- 2026-10-21 step 6 done (`675da1d`)
- 2026-10-21 step 7 done (`611b4f8`)
