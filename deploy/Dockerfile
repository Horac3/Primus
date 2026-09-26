# ---- Stage 1: builder -------------------------------------------------------
FROM golang:1.23 AS builder

WORKDIR /src

# Copy dependency manifests first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy full source.
COPY . .

# Build a fully static binary — no cgo, no libc dependency.
# modernc.org/sqlite is pure Go so CGO_ENABLED=0 is safe.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /primus ./cmd/primus

# ---- Stage 2: final image ---------------------------------------------------
# distroless/static has no shell, no package manager — smallest attack surface.
FROM gcr.io/distroless/static:nonroot

# Copy the single binary — templates and static assets are embedded inside it.
COPY --from=builder /primus /primus

# Copy the examples directory so "primus run examples/..." works out of the box.
COPY --from=builder /src/examples /examples

# Persistent data volume for the SQLite database file.
VOLUME /data

EXPOSE 8080

# Default: run the web server against the volume-mounted DB.
ENTRYPOINT ["/primus"]
CMD ["--addr", ":8080", "--db", "/data/primus.db"]
