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

## Running Tests locally on a Kind Cluster

### Deploy a Konflux Cluster

**Note:** Steps below are mostly similar to [the upstream doc](https://konflux-ci.dev/konflux-ci/docs/installation/install-local/), but it also includes the additional steps needed to apply custom changes and enabling of running e2e tests.

1. Clone [konflux-ci upstream repository](https://github.com/konflux-ci/konflux-ci) or fetch latest changes if you already have a clone.

2. Make sure you have the required tools installed and CPU/memory requirement as mentioned in [the prerequisites](https://konflux-ci.dev/konflux-ci/docs/installation/install-local/#prerequisites) 

3. Create a local kind cluster

```bash
KIND_MEMORY_GB=12 ./scripts/setup-kind-local-cluster.sh
```

4. Create a copy of the env file

```bash
cp scripts/deploy-local.env.template scripts/deploy-local.env
```

5. For webhook forwarding, use a gosmee channel and set the value in `deploy-local.env`

```bash
SMEE_CHANNEL=https://hook.pipelinesascode.com/<channel_id>
```
**Note:** Visiting https://hook.pipelinesascode.com/ will auto assign a channel id, will get to see in the browser URL, you may use it, or you may use a random alpha numeric string of length 12 (if its not already used by someone already). Use `hook.pipelinesascode.com` (gosmee) instead of `smee.io`. smee.io does not work with Forgejo because webhook signature validation fails.

6. Create a Github App to receive the webhook events from GitHub following [the documentation](https://konflux-ci.dev/konflux-ci/docs/guides/github-secrets/)
and set below values in `deploy-local.env`

```bash
GITHUB_PRIVATE_KEY_PATH=<path to private key downloaded earlier>
WEBHOOK_SECRET=<secret generated earlier>
GITHUB_APP_ID=<GitHub APP ID>
```

7. Install the created GitHub App on the repositories you want to use with Konflux or in your test organization having sample repositories.

8. Setup the quay organization where repositories for component images will be created. Also create a quay token of OAuth application with scopes
  -  Administer organizations
  -  Administer repositories
  - Create repositories

  Set the values in `deploy-local.env` file. For more details, you may refer [this page](https://konflux-ci.dev/konflux-ci/docs/guides/registry-configuration/#quayio-auto-provisioning-image-controller)

```bash
QUAY_ORGANIZATION=<Quay Org>
QUAY_TOKEN=<Quay Token>
```

**Note:** It is needed to enable image-controller component.


9. Update the default webhook URLs in the Konflux CR `operator/config/samples/konflux-e2e.yaml` to be used during deployment

```diff
   buildService:
     spec:
+      webhookURLs:
+        "": "https://hook.pipelinesascode.com/XXXXXXXX"
       # Skip TLS verification for PaC webhook URLs — the e2e environment
```

10. If you want to use custom build-service image, override it in `operator/pkg/manifests/build-service/manifests.yaml`

```diff
         command:
         - /manager
-        image: quay.io/konflux-ci/build-service:ea2cad62004f1c497ac7b5b784bd46bfc0409cc1
+        image: quay.io/susdas/build-service:2d761643b30f58b78c5d017a769a8027f7982433
         livenessProbe:
           httpGet:
```

11. Install the dependencies and create required secrets after setting up required environment variables.

```bash
export DEPLOY_LOCAL_SKIP_KIND=1
export KIND_CLUSTER=konflux
export KONFLUX_CR=operator/config/samples/konflux-e2e.yaml
export KONFLUX_READY_TIMEOUT=30m
export CONTAINER_TOOL=podman
export OPERATOR_INSTALL_METHOD=none

./scripts/deploy-local.sh
```

12. Move to the `operator` directory and run the konflux operator

```bash
cd operator
make install   # Install CRDs
make run       # Run the operator locally
```

13. Then, in another terminal, apply the Konflux CR:

```bash
kubectl apply -f operator/config/samples/konflux-e2e.yaml
```

14. Wait for konflux to be ready.

```bash
kubectl wait --for=condition=Ready konflux konflux --timeout=15m
```

### Running the E2E Tests

1. Switch to your `build-service` clone directory

2. Setup the environment needed by the e2e tests

```bash
# for running tests in upstream konflux deployed from konflux-ci/konflux-ci, no need to set it when running the tests in a konflux cluster deployed using infra-deployment script `hack/bootstrap-cluster.sh`
export TEST_ENVIRONMENT=upstream

# Set the git providers to run tests against.
# It can be a comma-separated list of provider prefixes: "gh,gl,gt" or "gh" or "gl,gt"
# If not set or empty, all registered providers will be used.
# Examples:
#   E2E_GIT_PROVIDERS=gh           -> only GitHub
#   E2E_GIT_PROVIDERS=gl           -> only GitLab
#   E2E_GIT_PROVIDERS=gh,gl,fj     -> all three (GitHub, GitLab, Forgejo)
#   E2E_GIT_PROVIDERS=""           -> all registered providers (default)
export E2E_GIT_PROVIDERS=gh

# the github organization test sample repositories are created and
# a github classic token with following scopes:
# - admin:org
# - delete_repo
# - repo
# - user
# - workflow
export MY_GITHUB_ORG=<github_org>
export GITHUB_TOKEN=<github token>

# same as QUAY_ORGANIZATION and QUAY_TOKEN values set in `deploy-local.env` file
export DEFAULT_QUAY_ORG=<quay org>
export DEFAULT_QUAY_ORG_TOKEN=<quay token>

# For running gitlab related tests, gitlab organization where samples repositories are created
# and a gitlab personal access legacy token with below scopes:
# - READ REPOSITORY
# - READ USER
# - READ API
# - WRITE REPOSITORY
# - API
# Note: the list of scopes is based on a working token, tests might work with a token with less scopes as well
export GITLAB_QE_ORG=<gitlab org>
export GITLAB_BOT_TOKEN=<gitlab bot token>

# For running codeberg related tests, codeberg organization where samples repositories are created
# and an access token with below permissions:
# - write:organization
# - write:issue
# - write:repository
# - read:user
export CODEBERG_QE_ORG=<codeberg org>
export CODEBERG_BOT_TOKEN=<codeberg bot token>

```

3. Run tests using ginkgo commands, for example, to run the `build-service` labelled tests

```bash
ginkgo -v --label-filter="build-service" ./e2e-tests/tests/
```

To run the `build-service` labelled tests in debug mode using `dlv`, use below commands:

```bash
cd e2e-tests/tests
dlv test -- -ginkgo.v -ginkgo.label-filter="build-service"
```