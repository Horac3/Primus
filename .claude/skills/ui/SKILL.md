---
name: ui
description: Build or change a screen or component with a short design pass, test-first behaviour, and a visual check by the ui-checker subagent.
argument-hint: "[screen or component, and what it should do]"
disable-model-invocation: true
---

Build or change this UI: $ARGUMENTS

## 1. Ground it in what exists (cheap)
- Read `DESIGN.md` at the repo root if it exists: it defines the visual language. If it does not exist and the project
  already has UI, offer to create one first (`/impeccable document` if Impeccable is installed; otherwise write it from
  the tokens and components you find, using the sections: theme and atmosphere, colour roles, typography, components,
  layout, elevation, do's and don'ts, responsive behaviour). It saves re-deriving the style in every session.
- Find the design system: tokens or theme file, Tailwind config, shared `components/`. Grep; do not read whole folders.
- Find the nearest existing screen or component that looks and behaves like the target. It is the reference for
  spacing, type, and patterns. Reuse its components.
- Find the dev server command and URL (package.json scripts, CLAUDE.md). If it is not running, ask the user to
  start it or start it in the background with the user's permission.

## 2. Design brief (5–8 lines, shown before coding)
- **Purpose and primary action**: what the user is here to do, and the one thing the screen should make obvious.
- **Layout**: regions, from top to bottom on mobile, then what changes on desktop.
- **States**: loading, empty, error, success: what each shows.
- **Components**: which existing ones are reused, and anything genuinely new (justify it).
- **Visual direction**: only for a new surface with no reference (and no `DESIGN.md` guidance). If the
  `frontend-design` or Impeccable skill is installed, follow it here (`/impeccable shape` plans this step). Otherwise: tone, type scale, one accent. Stay within the
  project's tokens. Avoid generic defaults (centred hero with a gradient, purple-on-white, a card grid for everything).
If the brief leaves a real choice open, ask. Otherwise proceed.

## 3. Build test-first
Behaviour first, with Testing Library (roles and labels): what renders in each state, what the actions do.
Then markup and styles. Follow the frontend rules (semantic HTML, focus, states, tokens).

## 4. Look at it
Ask the `ui-checker` subagent: the URL, the brief above, and what changed. If the `web-design-guidelines` skill is
installed, also run it **in a separate general-purpose subagent** on the changed files and take back only its
`file:line` findings: it fetches a long rule list that should not land in this conversation. Fix every `blocking` item, then run it
again. Report `polish` items to the user rather than gold-plating. Use at most 3 check rounds; if issues persist,
summarise them and ask.

## 5. Finish
List files changed, the states covered by tests, the final ui-checker verdict, and the screenshot paths so the
user can look themselves.
