#!/bin/bash
# exit immediately when a command fails
set -e
# only exit with zero if all commands of the pipeline exit successfully
set -o pipefail
# error on unset variables
set -u

command -v kubectl >/dev/null 2>&1 || { echo "kubectl is not installed. Aborting."; exit 1; }

export WORKSPACE BUILD_SERVICE_PR_OWNER BUILD_SERVICE_PR_SHA

WORKSPACE=$(dirname "$(dirname "$(readlink -f "$0")")");
export TEST_SUITE="build-service-suite"

# BUILD_SERVICE_IMAGE - build-service image built in openshift CI job workflow. More info about how image dependencies work in ci: https://github.com/openshift/ci-tools/blob/master/TEMPLATES.md#parameters-available-to-templates
# Container env defined at: https://github.com/openshift/release/blob/master/ci-operator/config/redhat-appstudio/build-service/redhat-appstudio-build-service-main.yaml
# Openshift CI generates the build service container image value as registry.build01.ci.openshift.org/ci-op-83gwcnmk/pipeline@sha256:8812e26b50b262d0cc45da7912970a205add4bd4e4ff3fed421baf3120027206. Need to get the image without sha.
export BUILD_SERVICE_IMAGE=${BUILD_SERVICE_IMAGE%@*}
export BUILD_SERVICE_IMAGE_REPO=${BUILD_SERVICE_IMAGE:-"quay.io/redhat-appstudio/build-service"}
# Tag defined at: https://github.com/openshift/release/blob/master/ci-operator/config/redhat-appstudio/build-service/redhat-appstudio-build-service-main.yaml
export BUILD_SERVICE_IMAGE_TAG=${BUILD_SERVICE_IMAGE_TAG:-"redhat-appstudio-build-service-image"}

if [[ -n "${JOB_SPEC}" && "${REPO_NAME}" == "build-service" ]]; then
    # Extract PR author and commit SHA to also override default kustomization in infra-deployments repo
    # https://github.com/redhat-appstudio/infra-deployments/blob/d3b56adc1bd2a7cf500793a7863660ea5117c531/hack/preview.sh#L88
    BUILD_SERVICE_PR_OWNER=$(jq -r '.refs.pulls[0].author' <<< "$JOB_SPEC")
    BUILD_SERVICE_PR_SHA=$(jq -r '.refs.pulls[0].sha' <<< "$JOB_SPEC")
fi

# Available openshift ci environments https://docs.ci.openshift.org/docs/architecture/step-registry/#available-environment-variables
export ARTIFACT_DIR=${ARTIFACT_DIR:-"/tmp/appstudio"}

function executeE2ETests() {
    # E2E instructions can be found: https://github.com/redhat-appstudio/e2e-tests
    # The e2e binary is included in Openshift CI test container from the dockerfile: https://github.com/redhat-appstudio/infra-deployments/blob/main/.ci/openshift-ci/Dockerfile
    curl https://raw.githubusercontent.com/redhat-appstudio/e2e-tests/main/scripts/e2e-openshift-ci.sh | bash -s

    # The bin will be installed in tmp folder after executing e2e-openshift-ci.sh script
    cd "${WORKSPACE}/tmp/e2e-tests"
    ./bin/e2e-appstudio --ginkgo.junit-report="${ARTIFACT_DIR}"/e2e-report.xml --ginkgo.focus="${TEST_SUITE}" --ginkgo.progress --ginkgo.v --ginkgo.no-color
}

curl https://raw.githubusercontent.com/redhat-appstudio/e2e-tests/main/scripts/install-appstudio-e2e-mode.sh | bash -s install

executeE2ETests
