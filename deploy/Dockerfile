# ---- Stage 1: builder -------------------------------------------------------
FROM golang:1.25 AS builder

WORKDIR /src

# Copy dependency manifests first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy full source.
COPY . .

# Build a fully static binary — no cgo, no libc dependency.
# modernc.org/sqlite is pure Go so CGO_ENABLED=0 is safe.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /primus ./cmd/primus

# Distroless has no shell, so create and own the data dir here,
# then copy it into the final stage with ownership intact.
RUN mkdir -p /data && chown 65532:65532 /data

# ---- Stage 2: final image ---------------------------------------------------
# distroless/static has no shell, no package manager — smallest attack surface.
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /primus /primus
COPY --from=builder --chown=65532:65532 /data /data

VOLUME /data
USER nonroot
ENTRYPOINT ["/primus"]
CMD ["--addr", ":8080", "--db", "/data/primus.db"]