---
paths:
  - "internal/credential/**"
  - "internal/store/pools.go"
  - "internal/web/pools.go"
  - "internal/insight/**"
  - "internal/author/**"
---

# Credentials, secrets and LLM payloads (Primus)

- Passwords, tokens and any field flagged as masked never appear in logs, run JSON, exports, error strings, the
  result page or any payload sent to a model. Store them; return masked values from every read API except the one
  the engine uses at dispatch.
- Analyzer and author payloads carry aggregates only: counts, percentiles, normalized URLs (`<url>`), finding
  codes. Never request or response bodies, headers, or credential fields.
- LLM output is untrusted: validate it against the data it was given (grounding check, tolerant numeric matching)
  and fall back to the rules analyzer on failure. Say which analyzer produced the result.
- Masking is decided per field by a stored flag (default from a name heuristic matched on word boundaries), not by
  substring on the field name.
- Any change here needs a test that greps captured logs, saved run JSON, the rendered page and the analyzer input
  for a known password and token, and finds none.
