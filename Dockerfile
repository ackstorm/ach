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
# Runtime image: alpine + git + ca-certificates.
#
# git is required at runtime by internal/sources/git/Fetcher, which the
# operator invokes to clone Plugin/Prompt/Artifact/PluginMarketplace
# upstreams. gcr.io/distroless/static:nonroot does NOT ship git, so any
# external-ref reconcile fails with "exec: git: not found". alpine is
# the smallest base that ships git + ca-certs via apk; the resulting
# image is ~40 MB (vs ~6 MB for distroless static), which is acceptable
# given the operator is the only mode that needs git.
#
# Security posture: CGO_ENABLED=0 so the ach binary has no libc deps;
# alpine's musl is unused. nonroot user (uid 65532) is created so the
# binary runs unprivileged, matching the prior distroless behaviour and
# the chart's securityContext.runAsNonRoot=true.
FROM alpine:3.20
RUN apk add --no-cache git ca-certificates && \
    addgroup -g 65532 -S nonroot && \
    adduser -u 65532 -S nonroot -G nonroot
WORKDIR /
COPY --from=builder /out/ach /ach
# Bake the SQL migrations into the image at /db/migrations so the
# `ach migrate` subcommand (Plan 08 init container) finds them at the
# default ACH_MIGRATIONS_PATH without needing a configmap mount.
COPY db/migrations /db/migrations
USER 65532:65532
ENTRYPOINT ["/ach"]
