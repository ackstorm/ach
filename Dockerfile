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
USER 65532:65532
ENTRYPOINT ["/ach"]
