# Build the manager binary
FROM golang:1.23 AS builder
ARG TARGETOS
ARG TARGETARCH
# VERSION must equal the tag this image is pushed/loaded as (#112): the manager derives
# its self-referential image defaults (lock-probe pods, injected egress-reporter) from
# it. The Makefile passes its dev tag; the release workflow passes the release tag. The
# default marks an unassembled build and resolves to images that do not exist (fails
# loudly rather than running stale release content).
ARG VERSION=v0.0.0-dev

WORKDIR /workspace
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a \
    -ldflags "-X github.com/grantbarry29/scrutineer/internal/version.Version=${VERSION}" \
    -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
