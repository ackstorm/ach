FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
# Explicit allow-list COPY — NEVER `COPY . .`. The build context is scoped to
# exactly the package this binary compiles, so (a) unrelated repo churn can't
# invalidate the builder layer cache, and (b) a stale `COPY . .` layer can't
# silently ship a pre-fix mock binary into the kind cluster. That second case
# actually bit us: the /__capture/reset mutex-copy crash fix was masked by a
# cached `COPY . .` layer and the old crashing binary kept serving. ach-mock is
# stdlib-only, so its own source is the entire build input.
COPY test/e2e/mock/ ./test/e2e/mock/
RUN CGO_ENABLED=0 go build -o /out/ach-mock ./test/e2e/mock
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/ach-mock /ach-mock
ENTRYPOINT ["/ach-mock"]
