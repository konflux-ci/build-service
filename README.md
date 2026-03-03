[![codecov](https://codecov.io/gh/redhat-appstudio/build-service/branch/main/graph/badge.svg)](https://codecov.io/gh/redhat-appstudio/build-service)

# Build Service

Build Service is a Kubernetes operator that creates and configures build pipelines for the [Konflux](https://konflux-ci.dev) platform. It is one of the core services required for a working Konflux installation, responsible for automating the creation and management of build pipeline definitions from Component custom resources.

## Overview

The Build Service monitors Component CRs (managed by the Hybrid Application Service or created manually) and automatically creates PipelineRun definitions that integrate with [Pipelines as Code (PaC)](https://pipelinesascode.com). The service handles the complete lifecycle of build pipelines including provisioning, configuration, execution triggering, and cleanup.

### Key Features

- **Automated Pipeline Provisioning**: Automatically creates and configures PipelineRun definitions in user repositories via merge requests
- **Pipelines as Code Integration**: Seamlessly integrates with PaC for event-driven pipeline execution
- **Webhook Management**: Sets up and manages webhooks for GitHub/GitLab repositories when not using GitHub Apps
- **Component Dependency Updates**: Automatically updates component dependencies through the nudging controller using Renovate
- **Build Pipeline Pruning**: Cleans up PipelineRuns when Components are deleted

## Architecture

The Build Service consists of three main controllers:

### Component Build Controller

Monitors Component CRs and manages the entire build pipeline lifecycle:
- Creates or reuses PaC Repository CRs
- Sets up webhooks for repositories (when GitHub App is not used)
- Creates merge requests with PipelineRun definitions in user repositories
- Creates component-specific service accounts (`build-pipeline-$COMPONENT_NAME`)
- Supports multiple operating modes: provision with MR, provision without MR, unprovision, and trigger builds

### PaC PipelineRun Pruner Controller

Removes PipelineRun CRs created for Components that are being deleted, ensuring proper cleanup based on the `appstudio.openshift.io/component` label.

### Component Dependency Update Controller (Nudging)

Monitors successful push PipelineRuns and automatically updates image references in dependent component repositories:
- Runs Renovate to update SHA references in Dockerfiles, YAML files, and Containerfiles
- Creates merge requests in dependent repositories
- Configurable via annotations and custom ConfigMaps

## Dependencies

Build Service depends on the following Konflux components:

### Required Dependencies

- **[Pipeline Service](https://github.com/konflux-ci/architecture/blob/main/architecture/core/pipeline-service.md)** (Core Service)
  - Provides Tekton Pipelines for pipeline execution
  - Provides Pipelines as Code for webhook-driven builds
  - Provides Tekton Chains for signing and attestations
  - Provides Tekton Results for pipeline logging and archival

- **[Image Controller](https://github.com/konflux-ci/architecture/blob/main/architecture/add-ons/image-controller.md)** (Add-on Service)
  - Generates container image repositories in the configured registry (e.g., Quay.io)
  - Creates robot accounts for image push/pull operations
  - Sets the `.spec.containerImage` field on Component CRs, which Build Service requires before provisioning pipelines

- **[Application API](https://github.com/konflux-ci/application-api)**
  - Provides Application and Component custom resource definitions
  - Component CRs are the primary input for Build Service

- **[Release Service](https://github.com/konflux-ci/release-service)** (Optional for nudging, but CRDs required)
  - The Component Dependency Update Controller reads ReleasePlanAdmission CRs to discover distribution repositories
  - When a build completes, nudging needs to know both build-time and release-time image repositories
  - This allows dependent components to reference the correct registry where images will be released
  - **Note**: Currently, the nudging controller cannot be disabled, so Release Service CRDs must be installed even if you don't use the nudging feature
  - If you don't need nudging, install only the Release Service CRDs (not the full controller) to avoid errors

## Installation

### Installing as Part of Konflux (Recommended)

Build Service is a **core component** of Konflux and is automatically installed when you deploy Konflux. It cannot function independently and should be installed as part of a complete Konflux deployment.

#### Option 1: Operator-Based Installation (Recommended)

Deploy Konflux using the Konflux Operator, which will automatically install Build Service and all its dependencies:

```bash
# Install the Konflux Operator
kubectl apply -f https://github.com/konflux-ci/konflux-ci/releases/latest/download/install.yaml

# Wait for operator to be ready
kubectl wait --for=condition=Available deployment/konflux-operator-controller-manager \
  -n konflux-operator --timeout=300s

# Deploy Konflux (which includes Build Service)
kubectl apply -f https://github.com/konflux-ci/konflux-ci/releases/latest/download/samples.tar.gz | \
  tar -xzO ./konflux_v1alpha1_konflux.yaml
```

For production deployments, customize the Konflux CR based on the [sample configurations](https://github.com/konflux-ci/konflux-ci/tree/main/operator/config/samples).

#### Option 2: Manual Installation (Legacy)

For environments that still require manual installation, deploy Konflux components in order:

```bash
# 1. Deploy Application API CRDs
kubectl apply -k https://github.com/konflux-ci/konflux-ci/konflux-ci/application-api

# 2. Deploy Pipeline Service dependencies (Tekton, PaC, Chains, Results)
# See https://github.com/konflux-ci/konflux-ci for complete instructions

# 3. Deploy Enterprise Contract
kubectl apply -k https://github.com/konflux-ci/konflux-ci/konflux-ci/enterprise-contract

# 4. Deploy Release Service (Build Service depends on Release CRDs)
kubectl apply -k https://github.com/konflux-ci/konflux-ci/konflux-ci/release --server-side

# 5. Deploy Build Service
kubectl apply -k https://github.com/konflux-ci/konflux-ci/konflux-ci/build-service
```

**Note:** This legacy method is deprecated. Use the operator-based installation for new deployments.

### Standalone Installation (Development Only)

For development and testing purposes, you can deploy just the Build Service operator:

```bash
# Install CRDs
make install

# Deploy the controller
make deploy IMG=<your-registry>/build-service:tag
```

**Important:** This will not result in a functional build system without the required dependencies listed above.

## Configuration

Build Service is configured through:

1. **Build Pipeline Config**: A ConfigMap in the controller's namespace that defines available build pipelines and their default parameters
2. **Component Annotations**: Control pipeline selection and provisioning behavior
   - `build.appstudio.openshift.io/request`: Operation mode (configure-pac, unconfigure-pac, trigger-pac-build)
   - `build.appstudio.openshift.io/pipeline`: Pipeline selection (name and bundle version)
   - `build.appstudio.openshift.io/build-nudge-files`: File patterns for nudging

3. **Environment Variables**: Fine-tune controller behavior (e.g., `IMAGE_TAG_ON_PR_EXPIRATION`)

## Development

### Prerequisites

- Go 1.21+
- kubectl
- kustomize
- Access to a Kubernetes cluster with Konflux dependencies installed

### Building

```bash
# Build the manager binary
make build

# Run tests
make test

# Build and push Docker image
make docker-build docker-push IMG=<your-registry>/build-service:tag
```

### Running Locally

```bash
# Install CRDs
make install

# Run controller locally (connects to your current kubeconfig context)
make run
```

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines on how to contribute to this project.

## Documentation

- [Architecture Documentation](https://github.com/konflux-ci/architecture/blob/main/architecture/core/build-service.md)
- [Konflux Documentation](https://konflux-ci.dev)
- [ADRs (Architectural Decision Records)](https://github.com/konflux-ci/architecture/tree/main/ADR)

## License

Apache License 2.0 - see [LICENSE](./LICENSE) for details.
