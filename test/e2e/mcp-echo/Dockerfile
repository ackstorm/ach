# syntax=docker/dockerfile:1.6
FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
# Explicit allow-list COPY — NEVER `COPY . .` (a stale whole-context layer can
# silently ship an old binary into the kind cluster; see test/e2e/mock/Dockerfile).
# ach-mcp-echo compiles only its own tree (main + the jwt subpackage); third-party
# deps (golang-jwt, mark3labs/mcp-go) arrive via the cached `go mod download`.
COPY test/e2e/mcp-echo/ ./test/e2e/mcp-echo/
RUN CGO_ENABLED=0 GOFLAGS="-trimpath" \
    go build -o /out/ach-mcp-echo ./test/e2e/mcp-echo

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/ach-mcp-echo /ach-mcp-echo
EXPOSE 9090
ENTRYPOINT ["/ach-mcp-echo"]
