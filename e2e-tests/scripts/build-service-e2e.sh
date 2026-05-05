#!/bin/bash

set -euo pipefail

load_envs() {
    local konflux_ci_secrets_file="/usr/local/konflux-ci-secrets"
    local konflux_infra_secrets_file="/usr/local/konflux-test-infra"

    declare -A config_envs=(
        [ENABLE_SCHEDULING_ON_MASTER_NODES]="false"
        [UNREGISTER_PAC]="true"
        [EC_DISABLE_DOWNLOAD_SERVICE]="true"
        [DEFAULT_QUAY_ORG]="redhat-appstudio-qe"
        [OCI_STORAGE_USERNAME]="$(jq -r '."quay-username"' ${konflux_infra_secrets_file}/oci-storage)"
        [OCI_STORAGE_TOKEN]="$(jq -r '."quay-token"' ${konflux_infra_secrets_file}/oci-storage)"
    )

    declare -A load_envs_from_file=(
        [DEFAULT_QUAY_ORG_TOKEN]="${konflux_ci_secrets_file}/default-quay-org-token"
        [QUAY_TOKEN]="${konflux_ci_secrets_file}/quay-token"
        [QUAY_OAUTH_USER]="${konflux_ci_secrets_file}/quay-oauth-user"
        [QUAY_OAUTH_TOKEN]="${konflux_ci_secrets_file}/quay-oauth-token"
        [E2E_PAC_GITHUB_APP_ID]="${konflux_ci_secrets_file}/pac-github-app-id"
        [E2E_PAC_GITHUB_APP_PRIVATE_KEY]="${konflux_ci_secrets_file}/pac-github-app-private-key"
        [PAC_GITHUB_APP_WEBHOOK_SECRET]="${konflux_ci_secrets_file}/pac-github-app-webhook-secret"
        [DOCKER_IO_AUTH]="${konflux_ci_secrets_file}/docker_io"
        [GITLAB_BOT_TOKEN]="${konflux_ci_secrets_file}/gitlab-bot-token"
        [SMEE_CHANNEL]="${konflux_ci_secrets_file}/smee-channel"
        [CODEBERG_BOT_TOKEN]="${konflux_ci_secrets_file}/codeberg-bot-token"
    )

    for var in "${!config_envs[@]}"; do
        export "$var"="${config_envs[$var]}"
    done

    for var in "${!load_envs_from_file[@]}"; do
        local file="${load_envs_from_file[$var]}"
        if [[ -f "$file" ]]; then
            export "$var"="$(<"$file")"
        else
            log "WARN" "Secret file for $var not found at $file"
        fi
    done
}

post_actions() {
    local exit_code=$?

    if [[ "$exit_code" == "124" ]]; then
        printf "\n\n" | tee -a "${ARTIFACT_DIR}/e2e-tests.log"
        log "ERROR" "The process for running tests timed out after $E2E_TIMEOUT" | tee -a "${ARTIFACT_DIR}/e2e-tests.log"
    fi

    exit "$exit_code"
}

trap post_actions EXIT

load_envs

export CUSTOM_DOCKER_BUILD_OCI_TA_PIPELINE_BUNDLE="${CUSTOM_DOCKER_BUILD_OCI_TA_PIPELINE_BUNDLE:-quay.io/konflux-ci/tekton-catalog/pipeline-docker-build-oci-ta:devel}"
log "INFO" "Using pipeline bundle: ${CUSTOM_DOCKER_BUILD_OCI_TA_PIPELINE_BUNDLE}"

LABEL_FILTER="build-service"
if [[ -n "${E2E_EXTRA_LABEL_FILTER:-}" ]]; then
    LABEL_FILTER="${LABEL_FILTER} && ${E2E_EXTRA_LABEL_FILTER}"
fi

GINKGO_PROCS="${GINKGO_PROCS:-5}"

log "INFO" "Tuning cluster resources for parallel e2e tests"
kubectl -n pipelines-as-code patch deployment pipelines-as-code-controller \
    --type=json -p '[
    {"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/memory","value":"512Mi"},
    {"op":"replace","path":"/spec/template/spec/containers/0/resources/requests/memory","value":"256Mi"}
]' 2>/dev/null && \
kubectl -n pipelines-as-code rollout status deployment/pipelines-as-code-controller --timeout=120s 2>/dev/null || \
log "WARN" "Could not patch PaC controller resources, continuing with defaults"

log "INFO" "Running build-service e2e tests with label filter: ${LABEL_FILTER}, procs: ${GINKGO_PROCS}"

cd /workspace/source/e2e-tests

go install github.com/onsi/ginkgo/v2/ginkgo

timeout "$E2E_TIMEOUT" ginkgo \
    -p \
    --procs="${GINKGO_PROCS}" \
    -v \
    --no-color \
    --output-interceptor-mode=none \
    --timeout=90m \
    --fail-on-empty \
    --label-filter="${LABEL_FILTER}" \
    --junit-report=e2e-report.xml \
    --output-dir="${ARTIFACT_DIR}" \
    ./tests/ \
    2>&1 | tee "${ARTIFACT_DIR}/e2e-tests.log"
