# Runs the test suite with the race detector inside the golang image (PowerShell).
# The default golang image ships gcc, which -race needs.
docker run --rm `
  -v "${PWD}:/src" `
  -v primus-gomod:/go/pkg/mod `
  -w /src `
  golang:1.25 `
  go test ./... -race @args
