---
paths:
  - "**/Dockerfile*"
  - "**/*compose*.yml"
  - "**/*compose*.yaml"
  - "**/.dockerignore"
---

# Docker and deployment (claude-kit)

- Production services join the external `nginx-proxy` network, use `expose` (never `ports`), and
  self-register with `VIRTUAL_HOST`, `VIRTUAL_PORT`, `LETSENCRYPT_HOST`, `LETSENCRYPT_EMAIL`.
  nginx-proxy and acme-companion run separately on the host; do not add them to app compose files.
- Dev compose may publish ports; keep dev and prod compose files separate.
- Pin base images by exact tag (and digest for production). Multi-stage builds; run as non-root.
- Secrets come from env or files mounted at runtime, never baked into images or committed.
- Native builds (cgo, node-gyp, `go test -race`) run in Docker or CI, not on the dev machine.
- Never run `docker compose down -v`, `docker volume rm` or `docker system prune`; ask the user.
