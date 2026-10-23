---
paths:
  - "**/*.tsx"
  - "**/*.jsx"
  - "**/*.vue"
  - "**/*.svelte"
  - "**/*.css"
  - "**/*.scss"
  - "**/*.html"
  - "**/*.tmpl"
  - "**/*.gohtml"
  - "**/*.templ"
  - "**/tailwind.config.*"
---

# Frontend (claude-kit)

- **`DESIGN.md` at the repo root, if present, is the source of truth** for colour, type, spacing, components, and
  do's and don'ts. Read it before visual work, and follow it over your own taste. If you change the visual language, update it.
- **Reuse before you build.** Look for an existing component, token, or utility first (grep `components/`, the
  theme or tokens file, the Tailwind config). Never hard-code a colour, spacing, radius, or font that a token covers.
- **No new UI dependency** (component kit, icon set, animation library) without asking.
- **Semantic HTML first:** `button` for actions, `a` for navigation, real `label`s for inputs, one `h1`,
  headings in order, landmarks (`header`, `nav`, `main`). ARIA only when HTML cannot express it.
- **Keyboard and focus:** everything clickable is reachable by Tab, has a visible focus style, and dialogs trap and restore focus.
- **Every data view has four states:** loading, empty, error, and success. Design all four, not just the happy path.
- **Responsive, mobile first:** works at 360px wide with no horizontal scroll; tap targets ≥ 24px (44px for primary actions).
- **Text contrast** of at least 4.5:1 (3:1 for large text). Never rely on colour alone to convey meaning.
- **Performance:** size images (`width`/`height` or aspect ratio), lazy-load below the fold, no layout shift on load.
- **Tests:** Testing Library queries by role and label (`getByRole('button', { name: … })`), not by class or test id
  unless there is no accessible handle. Test behaviour, not markup.
- **Look at it:** after a visible change, have the `ui-checker` subagent screenshot it (or use `/ui`).
  Do not call UI work done without a visual check.
