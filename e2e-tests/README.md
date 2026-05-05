# E2E Tests

This directory contains the end-to-end (E2E) test suite and CI infrastructure for `build-service`.

## Directory Structure

```
e2e-tests/
├── pipelines/      Tekton Pipeline definitions for CI
├── scripts/        Shell scripts executed by Tekton Tasks
├── tasks/          Tekton Task definitions
├── tests/          Go test files (Ginkgo test suite)
├── go.mod          Go module for the test suite
└── go.sum
```

### `pipelines/`

A single parameterized Tekton Pipeline (`konflux-e2e-tests.yaml`) that provisions a Kind cluster on AWS, deploys Konflux, runs a subset of tests filtered by provider, collects artifacts, and deprovisions the cluster.

The pipeline accepts two provider-specific parameters set by each IntegrationTestScenario:

| IntegrationTestScenario | `provider-tag` | `e2e-label-filter` |
|-------------------------|---------------|-------------------|
| GitHub | `github` | `github` |
| GitLab | `gitlab` | `gitlab` |
| Forgejo | `forgejo` | `forgejo` |

### `tasks/`

Tekton Task definitions used by the pipelines. The `build-service-e2e` task clones this repository, sets up the test environment, and runs the Ginkgo test suite.

### `scripts/`

Shell scripts invoked by the Tekton Tasks to configure the environment, load secrets, and execute the test runner.

### `tests/`

Ginkgo-based E2E tests that validate build-service functionality against a running Konflux cluster.
Tests are organized by feature and use Ginkgo labels for filtering by git provider (`github`, `gitlab`, `forgejo`) and feature area (`pac-build`, `renovate`, `multi-component`, etc.).

Tests consume shared utilities from [`github.com/konflux-ci/e2e-tests`](https://github.com/konflux-ci/e2e-tests).

## Running Tests Locally

From the repository root:

```bash
make test/e2e
```

To run a specific provider subset:

```bash
cd e2e-tests
go run github.com/onsi/ginkgo/v2/ginkgo@latest -v --label-filter="build-service && github" ./tests/
```
