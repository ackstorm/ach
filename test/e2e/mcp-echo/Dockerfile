# syntax=docker/dockerfile:1.6
FROM golang:1.26 AS builder
WORKDIR /src
COPY test/e2e/mcp-echo/go.mod test/e2e/mcp-echo/go.sum ./
RUN go mod download
# Explicit allow-list COPY — NEVER `COPY . .` (see test/e2e/mock/Dockerfile).
COPY test/e2e/mcp-echo/ ./
RUN CGO_ENABLED=0 GOFLAGS="-trimpath" go build -o /out/ach-mcp-echo .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/ach-mcp-echo /ach-mcp-echo
EXPOSE 9090
ENTRYPOINT ["/ach-mcp-echo"]
