---
paths:
  - "**/domain/**"
  - "**/core/**"
  - "**/ports/**"
  - "**/adapters/**"
---

# Hexagonal architecture (claude-kit)

- The domain (entities, value objects, policies, use cases) imports no framework, ORM, HTTP,
  queue, or vendor SDK. Only the language standard library and other domain code.
- The domain declares **ports** (interfaces) for what it needs: storage, payments, email, clocks,
  ID generation. **Adapters** implement them outside the domain. Dependencies point inward only.
- Time and randomness come in through ports or parameters, so domain logic stays deterministic.
- Domain logic is tested as pure functions and in-memory fakes of ports. Never mock domain types.
- Validation at the edge (HTTP, queue, DB rows) turns untrusted input into domain types once.
  Inside the domain, values are already valid.
- Money is integer minor units plus an ISO 4217 currency code. Never floats.
- If a change needs a framework type inside the domain, stop: add a port instead and say so in the plan.
