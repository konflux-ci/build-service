# Build the manager binary
# For more details and updates, refer to
# https://catalog.redhat.com/en/software/containers/ubi10/go-toolset/677d3db01d4db033edfda594
FROM registry.access.redhat.com/ubi10/go-toolset:1.26.10@sha256:557f358c83892a8b94fb72649a99e1c303a625d240547fb97a9cbc1620e808c0 AS builder
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
# https://catalog.redhat.com/en/software/containers/ubi10/ubi-minimal/66f1504a379b9c2cf23e145c
FROM registry.access.redhat.com/ubi10/ubi-minimal:10.2@sha256:ab67cd02caf8706e34a695a1432ebe965934f6128ec352a6c3f4048b43a4fb5e
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
