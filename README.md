# Build Service

*A Kubernetes operator that creates and manages build pipelines*

The **Build Service** is a core component of the [Konflux](https://konflux-ci.dev) platform. It automates the process of building container images for your components by responding to changes in Component custom resources (CRs). When a new component is onboarded or updated, the Build Service interprets annotations and configuration and then generates Tekton **PipelineRuns** to build your source code into container images. Its role is often summarized as **"Component in, PipelineRun out."**

---

## Table of Contents

- [Overview](#overview)
- [Dependencies](#dependencies)
- [Customizing Build Pipelines](#customizing-build-pipelines)
- [Controllers](#controllers)
  - [Component Build Controller](#component-build-controller)
  - [PaC PipelineRun Pruner Controller](#pac-pipelinerun-pruner-controller)
  - [Component Dependency Update (Nudging) Controller](#component-dependency-update-nudging-controller)
- [Installing and Running the Build Service](#installing-and-running-the-build-service)
- [Verifying the Installation](#verifying-the-installation)
- [Contributing](#contributing)
- [License](#license)

---

## Overview

The Build Service watches Component Custom Resources (CRs) within the namespace and orchestrates the entire build lifecycle. It is implemented as a Kubernetes operator and runs inside the cluster. When it detects a new or updated component, it:

- **Determines the build pipeline:** It consults a ConfigMap of available Tekton pipelines (provided by the cluster administrator) and the `build.appstudio.openshift.io/pipeline` annotation on the component to pick the appropriate pipeline. If no annotation is set, a default pipeline from the ConfigMap is used.
- **Generates PipelineRun YAML:** It creates `PipelineRun` definitions for both push events (builds on the default branch) and pull/merge requests. These `PipelineRuns` include parameters such as `git-url`, `revision` (commit SHA), `output-image`, `dockerfile`, and other context settings.
- **Integrates with Pipelines as Code (PaC):** It either commits the generated `PipelineRun` YAML files to the component's repository or configures Pipelines as Code to run them automatically, depending on the mode selected. This enables builds to be triggered by repository webhooks or GitHub/GitLab apps.
- **Manages credentials:** It ensures that service accounts, secrets, and image repository permissions are created so that Tekton tasks can pull sources and push images. It also manages incoming webhook secrets and supports both GitHub App and webhook-based PaC integration.
- **Cleans up and updates dependencies:** It removes `PipelineRuns` when components are deleted and optionally performs "nudging" (dependency updates) to update downstream components' `Dockerfile`s or YAML files with the new image digest.

---

## Dependencies

The Build Service depends on the following Konflux services and external tools:

| Dependency | Purpose |
|---|---|
| **Pipeline Service** | Provides Tekton pipeline execution and logging. |
| **Image Controller** | Creates container image repositories and robot accounts for each component so that built images can be stored securely. |

> **Note:** Ensure that the Image Controller and Pipeline Service are installed in your cluster before deploying the Build Service.

Because pipeline definitions come from the `build-service` ConfigMap, ensure that the ConfigMap (usually named `build-pipeline-config`) is present in the `build-service` namespace with entries for the pipelines you wish to use.

---

## Customizing Build Pipelines

The Build Service does not hard-code its Tekton pipelines. Instead it reads a **ConfigMap** named `build-pipeline-config` to determine which pipelines can be selected when onboarding components.

The default deployment ships with pre-defined pipelines, each tailored to different use cases. Current examples include:

- `docker-build`
- `docker-build-oci-ta`
- `docker-build-multi-platform-oci-ta`
- `fbc-builder`
- `tekton-bundle-builder-oci-ta`

> **Note:** This list may change across releases. Always check the `build-pipeline-config` ConfigMap in your cluster for the current set of available pipelines.

You can inspect the tasks included in these pipelines using the `tkn bundle` CLI against the published bundles.

To use newer versions of these pipelines or to add your own:

1. Create a new pipeline definition.
2. Push it to a registry as a Tekton bundle (e.g., via `tkn bundle push`).
3. Reference it in the `build-pipeline-config` ConfigMap under a unique key.

Once added, the pipeline becomes available for selection via the `build.appstudio.openshift.io/pipeline` annotation on your Component CR.

For full instructions, see the [enabling builds documentation](https://konflux-ci.dev/docs/installing/enabling-builds/).

---

## Controllers

The Build Service consists of several controllers. Each controller reconciles a particular aspect of the build lifecycle.

### Component Build Controller

The heart of the Build Service. It monitors Component CRs and responds to annotations on these CRs.

#### Operating Modes

The controller operates in different modes based on the `build.appstudio.openshift.io/request` annotation. If no annotation is set, it defaults to `configure-pac` and treats the component as newly onboarded.

| Annotation Value | Behavior |
|---|---|
| `configure-pac` *(default)* | Sets up webhooks (if a GitHub/GitLab app isn't used) and commits the generated `PipelineRun` definitions to the user's repository. Marks the component as "enabled" and records the merge request link in the `build.appstudio.openshift.io/status` annotation. |
| `configure-pac-no-mr` | Same as above but does not create a merge request; you must commit the `PipelineRun` YAML yourself. |
| `unconfigure-pac` | Removes the webhooks and pipeline files from the repository, cleaning up after a component is deleted. |
| `trigger-pac-build` | Re-runs the push `PipelineRun` (useful for rerunning failed builds). |

The controller also labels the generated `PipelineRuns` with the component name and ensures that a dedicated service account (`build-pipeline-<component-name>`) exists for each component.

#### PipelineRun Selection

Pipeline selection is controlled via the `build.appstudio.openshift.io/pipeline` annotation on the Component. The annotation value is a JSON string with `name` and `bundle` fields. For example:

```json
{ "name": "docker-build", "bundle": "latest" }
```

If the specified pipeline is missing from the ConfigMap, the controller sets the component's `build.appstudio.openshift.io/status` annotation to an error state.

#### PipelineRun Parameters

The Build Service populates several parameters in the generated `PipelineRuns`:

| Parameter | Description |
|---|---|
| `git-url` | Template that resolves to the component's repository URL (`{{source_url}}`). |
| `revision` | Template that resolves to the commit SHA (`{{revision}}`). |
| `output-image` | The container image reference from `spec.containerImage`. Tag is `:{{revision}}` for push pipelines and `:on-pr-{{revision}}` for pull/merge requests. |
| `image-expires-after` | Optional; controls expiration of images built for pull requests. |
| `dockerfile` | Path to the `Dockerfile` (defaults to `Dockerfile` if not set). |
| `path-context` | Optional path to a subdirectory if the Docker context is not the repository root. |

---

### PaC PipelineRun Pruner Controller

This controller removes `PipelineRuns` when a component is deleted. It identifies `PipelineRuns` by the `appstudio.openshift.io/component` label and ensures that old build histories don't clutter the cluster.

---

### Component Dependency Update (Nudging) Controller

The nudging controller monitors successful push `PipelineRuns` and updates other components that depend on the newly built image. Relationships are defined in the component's `spec.build-nudges-ref`.

When a build finishes, the controller runs [Renovate](https://docs.renovatebot.com/) on dependent repositories to open pull requests that update file references to the new image digest.

> **Note:** Whether Renovate is deployed and configured is platform-dependent. In managed Konflux environments it is handled as part of the platform. In standalone deployments, refer to your platform setup documentation.

Users can customize which files are nudged using annotations on the `PipelineRun` or a custom ConfigMap.

---

## Installing and Running the Build Service

### Production Deployments

In most Konflux deployments, the Build Service is installed automatically via the Konflux GitOps/installer. No manual steps are required. Refer to the [Konflux installation guide](https://konflux-ci.dev/docs/installing/) for details.

### Development / Standalone Testing

For development or standalone testing, you can deploy the Build Service yourself using the provided `Makefile`.

**Prerequisites:**
- A running Kubernetes or OpenShift cluster with `kubectl`/`oc` access.
- The Image Controller and Pipeline Service installed in the cluster.
- A container image registry accessible to your cluster.

**Steps:**

1. **Install CRDs:**
   ```bash
   make install
   ```

2. **Deploy the controller:**
   ```bash
   make deploy IMG=<your-container-image>
   ```

3. **Run locally** (without deploying to the cluster):
   ```bash
   make run
   ```

4. **Undeploy:**
   ```bash
   make undeploy
   ```

By default, the operator runs in the `build-service` namespace.

---

## Verifying the Installation

After deploying, verify that the Build Service is running correctly:

```bash
# Check that the controller manager deployment is healthy
kubectl get deploy -n build-service

# Check that the operator pod is running
kubectl get pods -n build-service

# Check operator logs for errors (standard kubebuilder layout)
kubectl logs -n build-service deploy/build-service-controller-manager -c manager --tail=100

# Confirm the build-pipeline-config ConfigMap is present
kubectl get configmap build-pipeline-config -n build-service
```

A healthy deployment will show the controller manager in `Available` state with its pod in `Running`, and no error-level log entries on startup.

---

## Contributing

We welcome contributions! Please review [`CONTRIBUTING.md`](./CONTRIBUTING.md) for details on our workflow, including:

- Setting up your local development environment (`make run` for local testing without deploying to a cluster).
- PR and issue labeling conventions.
- Running tests before submitting (`make test`).

Report issues via the [GitHub issue tracker](https://github.com/konflux-ci/build-service/issues) and discuss major changes with maintainers before opening large pull requests. Please follow the [Konflux code of conduct](https://github.com/konflux-ci/community/blob/main/CODE_OF_CONDUCT.md).

---

## License

This project is licensed under the [Apache 2.0 License](./LICENSE).
