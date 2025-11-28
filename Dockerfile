# Build the manager binary
# For more details and updates, refer to
# https://catalog.redhat.com/software/containers/ubi9/go-toolset/61e5c00b4ec9945c18787690
FROM registry.access.redhat.com/ubi9/go-toolset:1.24.6-1762230058 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG ENABLE_COVERAGE=false

USER 1001

WORKDIR /workspace
# Copy the Go Modules manifests
COPY --chown=1001:0 go.mod go.mod
COPY --chown=1001:0 go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY --chown=1001:0 . .

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN if [ "$ENABLE_COVERAGE" = "true" ]; then \
        echo "Building with coverage instrumentation..."; \
        CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -cover -covermode=atomic -tags=coverage -o manager ./cmd; \
    else \
        echo "Building production binary..."; \
        CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go; \
    fi

# Use ubi-minimal as minimal base image to package the manager binary
# For more details and updates, refer to
# https://catalog.redhat.com/software/containers/ubi9/ubi-minimal/615bd9b4075b022acc111bf5
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.6-1755695350
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

# Required for ecosystem-cert-preflight-checks
# https://access.redhat.com/documentation/en-us/red_hat_software_certification/2024/html-single/red_hat_openshift_software_certification_policy_guide/index#assembly-requirements-for-container-images_openshift-sw-cert-policy-introduction
COPY LICENSE /licenses/LICENSE

LABEL description="Konflux Build Service operator"
LABEL io.k8s.description="Konflux Build Service operator"
LABEL io.k8s.display-name="konflux-build-service-operator"
LABEL io.openshift.tags="konflux"
LABEL summary="Konflux Build Service"
LABEL name="konflux-build-service"
LABEL com.redhat.component="konflux-build-service-operator"

ENTRYPOINT ["/manager"]
