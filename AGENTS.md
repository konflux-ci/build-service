This file provides guidance on the Konflux Build Service operator.
The operator is a part of Konflux project.
The operator is responsible for Konflux Components.

Build Service doesn't build anything on its own, but it manages things to make builds happen.
Actual builds are done by Pipelines as Code (PaC).

## High-Level Project Structure

- `cmd/main.go` operator entrypoint, contains startup logic and global settings.
- `internal/controller` controllers, main reconcile logic.
- `pkg/boerrors` operator specific errors, each error has unique ID.
- `pkg/bometrics` metrics and availability probes.
- `pkg/common` shared between controllers and other packages utils.
- `pkg/git` clints to work with git providers.
- `pkg/pacwebhook` optional mapping between git repository URL and webhook URL.
   Used when cluster is behind a firewall.
   Then git events sent to a proxy (e.g. smee.io) and then reach PaC endpoint within the cluster.
- `*_unit_test.go` unit tests.
- `internal/controller/suite_util_test.go` common utilities used in integration tests (envtest).
- `export` exported constants for e2e tests.
- `e2e-tests` e2e tests, separate go project, require a cluster with installed Konflux to run.
   Agents shouldn't run those.

## Controllers

### Component build controller

Main controller of the operator.
Responsible for Components onboarding and offboarding:
- Manages in cluster k8s resources:
  - PaC Repository CR and related secrets.
  - Build pipeline Service Account.
  - and other...
- Component git repo preparation, mainly PaC webhook creation / removal.
- Build pipeline configuration proposal / removal PRs (in `.tekton` directory).

Also, responsble for push build pipelines re-runs.

### Component dependency update controller

Watches build pipelines in cluster and on a successful push pipeline checks BuildNudgesRef in the Component spec,
creating PRs into source git repository of Components listed in BuildNudgesRef.

Also known as nudge controller.
The controller is in maintainence mode, only bug fixes, no new features.

### PaC pipelinerun pruner controller

Removes pipeline runs related to just deleted Component.
Unlikely needs any changes.

## Git providers and their clients

Build Service operator works with git repositories hosted on remote git services.
Controllers use single git interface defined in `pkg/git/gitprovider`,
`pkg/git/gitproviderfactory` contains a factory that creates required git client instance.
Each get provider has own client implementation in a separate package.
Supported providers are:
- `github`
- `gitlab`
- `forgejo`

Only `github` supports applications (PaC GitHub application manages events, so webhook configuration is not needed if application is used),
however, stabdard webhook config is also supported for GitHub.

## Component old and new model

Currently, the operator supports old Component model (v1) and new Component model (v2).
Eventually, only new Component model (v2) should remain, now we are in transitional period.

Main new Component model advantages:
- Support for multiple product versions per Component unlike only one version in old model.
- Component fully configured via its spec instead of annotations in old model.
- Actions are requested via spec too instead of annotations in old model.
- Support for additional features, like temporal build disabling for a branch.

## Development

After making any code changes, always make sure that:
- unit and integration tests (envtest) pass (run with `make test` after all changes made).
- all linters pass (run with `make lint`).
