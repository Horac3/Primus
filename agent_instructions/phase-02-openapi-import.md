# Phase 2 — OpenAPI import

## Goal

Turn a swagger/OpenAPI spec (fetched by URL or uploaded as JSON) into
`RequestDefinition` rows in storage, using Phase 1's model and store
packages. No web UI yet, no test engine yet — this phase is the
importer only, exercised through tests and a small internal API.

## Scope

1. `internal/importer`:
   - Add `kin-openapi` as a dependency
     (`github.com/getkin/kin-openapi`) for parsing.
   - `ImportFromURL(ctx context.Context, url string) ([]model.RequestDefinition, error)`
     — fetches the spec, parses it, returns definitions. Does not
     persist — persistence is the caller's job (keeps this testable
     without a live DB).
   - `ImportFromBytes(data []byte) ([]model.RequestDefinition, error)`
     — same parsing, for uploaded/local specs. `ImportFromURL` should
     be a thin wrapper: fetch bytes, delegate to this.
   - For each operation in the spec, produce one `RequestDefinition`:
     - `Method`, `URLTemplate` (path with `{param}` placeholders kept
       as-is, matching Go's own path-param convention so later phases
       don't need to reconcile two placeholder syntaxes).
     - Required path/query params become `ParamSpec` entries with
       `Static` left empty and a TODO-marker `Dynamic` default (random
       int 1-1000 for integer types, a short random string for string
       types) — good enough to run a test immediately after import,
       user can edit later.
     - Enum-constrained query params (e.g. status:
       available/pending/sold) get their enum values captured so a
       future UI can render a select instead of free text — store
       this on `ParamSpec` even if nothing reads it yet this phase.
     - Request body schema, when present, generates a stub JSON body
       via the schema's example if present, else a minimally valid
       object from required properties and their types.
     - `Auth`: if the operation has a security requirement, set
       `AuthConfig.Type` to match (api_key/bearer/basic) — do not
       implement login-chained auth here, that's Phase 5's scope,
       just record the requirement so it isn't silently dropped.

2. Error handling: a spec that fails to parse (malformed JSON,
   invalid OpenAPI) returns a clear error, not a partial/silent
   result. A spec with zero operations is a valid (empty slice)
   result, not an error.

## Follow the standing rules

TDD/AAA per testing.md — write the failing test against a small
literal OpenAPI JSON fixture before writing the parser logic, not
against the full Petstore spec first. Add the real Petstore spec as
a second-tier integration test once the unit-level parsing works, so
a real-world spec's edge cases (nested schemas, `$ref`, arrays) get
exercised too. Session-logging.md and git-commits.md apply as before.

## Acceptance conditions (must ALL be true before phase 2 is done)

- [ ] `ImportFromBytes` has passing unit tests against at least 3
      hand-written minimal OpenAPI fixtures: one GET with a path
      param, one GET with an enum query param, one POST with a body
      schema.
- [ ] `ImportFromBytes` has a passing test against the real
      `https://petstore.swagger.io/v2/swagger.json` spec (fetched
      once, saved as a testdata fixture — do not hit the network on
      every test run), producing the expected count of
      RequestDefinitions and correct method/path for
      `getPetById`, `addPet`, and `findPetsByStatus`.
- [ ] `ImportFromURL` has a passing test using an `httptest.Server`
      standing in for the remote spec host (not a real network call
      in the test suite).
- [ ] Malformed input returns an error, verified by a test.
- [ ] `go test ./... -race` passes.
- [ ] `go build ./...` succeeds.
- [ ] bob_sessions/ screenshot for this phase exists.
- [ ] One git commit exists for this phase, formatted per
      git-commits.md.

Do not begin Phase 3 (load-test engine) until every box above is
checked.
