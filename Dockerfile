# Build the manager binary
FROM golang:1.25 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build
# the GOARCH has no default value to allow the binary to be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot

ARG VERSION=latest
ARG REVISION=""
ARG CREATED=""

LABEL org.opencontainers.image.title="azure-devops-agent-operator" \
      org.opencontainers.image.description="A Kubernetes operator for elastically-scalable Azure DevOps self-hosted agents" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/amaanx86/azure-devops-agent-operator" \
      org.opencontainers.image.documentation="https://github.com/amaanx86/azure-devops-agent-operator#readme" \
      org.opencontainers.image.authors="Amaan Ul Haq Siddiqui" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="amaanx86" \
      org.opencontainers.image.url="https://github.com/amaanx86/azure-devops-agent-operator" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"

WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
