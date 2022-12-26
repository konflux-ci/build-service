# Build the manager binary
FROM registry.access.redhat.com/ubi8/go-toolset:1.18.4-8 as builder

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY controllers/ controllers/
COPY api/ api/
COPY pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager main.go

# Use ubi-minimal as minimal base image to package the manager binary
# Refer to https://catalog.redhat.com/software/containers/ubi8-minimal/5c64772edd19c77a158ea216 for more details
FROM registry.access.redhat.com/ubi8/ubi-minimal:8.7-1031
COPY --from=builder /opt/app-root/src/manager /
USER 65532:65532

ENTRYPOINT ["/manager"]
