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

## Running Tests locally on a kind cluster

### Deploy a konflux cluster

**Note:** Steps below are mostly similar to [the upstream doc](https://konflux-ci.dev/konflux-ci/docs/installation/install-local/), but it also includes the additional steps needed to apply custom changes and enabling of running e2e tests.

1. Clone [konflux-ci upstream repository](https://github.com/konflux-ci/konflux-ci) or fetch latest changes if you already have a clone.

2. Make sure you have the required tools installed and CPU/memory requirement as mentioned in [the prerequisites](https://konflux-ci.dev/konflux-ci/docs/installation/install-local/#prerequisites) 

3. Create a local kind cluster

```
KIND_MEMORY_GB=12 ./scripts/setup-kind-local-cluster.sh
```

4. Create a copy of the env file

```
cp scripts/deploy-local.env.template scripts/deploy-local.env
```

5. For webhook forwarding, use a gosmee channel and set the value in `deploy-local.env`

```
SMEE_CHANNEL=https://hook.pipelinesascode.com/<channel_id>
```
**Note:** Use `hook.pipelinesascode.com` (gosmee) instead of `smee.io`. smee.io does not work with Forgejo because webhook signature validation fails.

6. Create a Github App to receive the webhook events from GitHub following [the documentation](https://konflux-ci.dev/konflux-ci/docs/guides/github-secrets/)
and set below values in `deploy-local.env`

```
GITHUB_PRIVATE_KEY_PATH=<path to private key downloaded earlier>
WEBHOOK_SECRET=<secret generated earlier>
GITHUB_APP_ID=<GitHub APP ID>
```

7. Install the created GitHub App on the repositories you want to use with Konflux or in your test organization having sample repositories.

8. Setup the quay repository to be used in the e2e tests and set the values in `deploy-local.env` file

```
QUAY_ORGANIZATION=<Quay Org>
QUAY_TOKEN=<quay token>
```

**Note:** These are same as the values we used to set [here](https://github.com/konflux-ci/e2e-tests/blob/c03c22276fdadaf3e9bf63d56829c4f7c22ad385/default.env#L11-L17) and needed to enable image-controller component.


9. Update the default webhook URLs in the Konflyx CR `operator/config/samples/konflux-e2e.yaml` to be used during deployment

```diff
   buildService:
     spec:
+      webhookURLs:
+        "": "https://hook.pipelinesascode.com/XXXXXXXX"
       # Skip TLS verification for PaC webhook URLs — the e2e environment
```

10. Do the custom build-service image override in `operator/pkg/manifests/build-service/manifests.yaml`

```diff
         command:
         - /manager
-        image: quay.io/konflux-ci/build-service:ea2cad62004f1c497ac7b5b784bd46bfc0409cc1
+        image: quay.io/konflux-ci/build-service:2d761643b30f58b78c5d017a769a8027f7982433
         livenessProbe:
           httpGet:
```

11. Install the dependencies and create required secrets after setting up required environment variables.

```
export DEPLOY_LOCAL_SKIP_KIND=1
export KIND_CLUSTER=konflux
export KONFLUX_CR=operator/config/samples/konflux-e2e.yaml
export KONFLUX_READY_TIMEOUT=30m
export CONTAINER_TOOL=podman
export OPERATOR_INSTALL_METHOD=local

./scripts/deploy-local.sh
```

12. Move to the `operator` directory and run the konflux operator

```
cd operator
make install   # Install CRDs
make run       # Run the operator locally
```

13. Then, in another terminal, apply the Konflux CR:

```
kubectl apply -f operator/config/samples/konflux-e2e.yaml
```

14. Wait for konflux to be ready.

```
kubectl wait --for=condition=Ready konflux konflux --timeout=15m
```

### Running the e2e tests

1. Switch to your `build-service` clone directory

2. Setup the environment needed by the e2e tests

```
# for running tests in upstream konflux environment
export TEST_ENVIRONMENT=upstream

# git provider you want to run the tests for, set accordingly, possible values are gh, gl or fj
export E2E_GIT_PROVIDERS=gh

# github org the sample repositories created in and the token for the git client to interact with github
export MY_GITHUB_ORG=<github_org>
export GITHUB_TOKEN=<github token>

# same quay org and quay token values set in `deploy-local.env` file
export DEFAULT_QUAY_ORG=<quay org>
export DEFAULT_QUAY_ORG_TOKEN=<quay token>

# for running gitlab related tests
export GITLAB_QE_ORG=<gitlab org>
export GITLAB_BOT_TOKEN=<gitlab bot token>

# for running codeberg related tests
export CODEBERG_QE_ORG=<codeberg org>
export CODEBERG_BOT_TOKEN=<codeberg bot token>

```

3. Run tests using ginkgo commands, for example, to run the `build-service` labelled tests

```
ginkgo -v --label-filter="build-service" ./e2e-tests/tests/
```