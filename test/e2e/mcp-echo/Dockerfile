# syntax=docker/dockerfile:1.6
FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOFLAGS="-trimpath" \
    go build -o /out/ach-mcp-echo ./test/e2e/mcp-echo

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/ach-mcp-echo /ach-mcp-echo
EXPOSE 9090
ENTRYPOINT ["/ach-mcp-echo"]
