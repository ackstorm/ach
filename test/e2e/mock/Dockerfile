FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ach-mock ./test/e2e/mock
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/ach-mock /ach-mock
ENTRYPOINT ["/ach-mock"]
