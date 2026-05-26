# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION} AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build \
      -trimpath \
      -ldflags="-s -w -X github.com/ackstorm/ach/cmd/ach/cmd.Version=${VERSION}" \
      -o /out/ach \
      ./cmd/ach
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /out/ach /ach
# Bake the SQL migrations into the image at /db/migrations so the
# `ach migrate` subcommand (Plan 08 init container) finds them at the
# default ACH_MIGRATIONS_PATH without needing a configmap mount.
COPY db/migrations /db/migrations
USER 65532:65532
ENTRYPOINT ["/ach"]
