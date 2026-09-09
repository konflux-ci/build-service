package constants

import (
	"time"

	"github.com/konflux-ci/build-service/e2e-tests/pkg/utils"
)

type BuildPipelineType string

const (
	// env used set test namespace if you want to use other than randomly generated one
	E2E_NAMESPACE_ENV string = "E2E_NAMESPACE"

	// A github token used to interact with github the tests.
	GITHUB_TOKEN_ENV string = "GITHUB_TOKEN"

	// The github organization where all the test sample repositories resides
	GITHUB_E2E_ORGANIZATION_ENV string = "MY_GITHUB_ORG"

	// gitlab bot token used to run gitlab related tests.
	GITLAB_BOT_TOKEN_ENV string = "GITLAB_BOT_TOKEN"

	// The gitlab API URL used to run gitlab related tests
	GITLAB_API_URL_ENV string = "GITLAB_API_URL"

	// Codeberg bot token is used to run tests against codeberg.org
	CODEBERG_BOT_TOKEN_ENV string = "CODEBERG_BOT_TOKEN"

	// Codeberg base URL used to run e2e tests
	CODEBERG_API_URL_ENV string = "CODEBERG_API_URL"

	// Codeberg org which owns the test repositories
	CODEBERG_QE_ORG_ENV string = "CODEBERG_QE_ORG"

	DefaultGithubOrg      = "redhat-appstudio-qe"
	DefaultGilabGroupId   = "85150202" // group id for "konflux-qe"
	DefaultGitLabAPIURL   = "https://gitlab.com/api/v4"
	DefaultCodebergAPIURL = "https://codeberg.org"
	DefaultCodebergQEOrg  = "konflux-qe"

	CheckrunConclusionSuccess = "success"
	CheckrunConclusionFailure = "failure"
	CheckrunStatusCompleted   = "completed"
	CheckrunConclusionNeutral = "neutral"

	DockerBuild                   BuildPipelineType = "docker-build"
	DockerBuildOciTA              BuildPipelineType = "docker-build-oci-ta"
	DockerBuildOciTAMin           BuildPipelineType = "docker-build-oci-ta-min"
	DockerBuildMultiPlatformOciTa BuildPipelineType = "docker-build-multi-platform-oci-ta"
	FbcBuilder                    BuildPipelineType = "fbc-builder"

	// Label for marking a namespace as a tenant namespace
	TenantLabelKey   string = "konflux-ci.dev/type"
	TenantLabelValue string = "tenant"

	// A cluster role used to be bound to a user that has admin access to all Konflux resources in a specific namespace
	// https://github.com/konflux-ci/konflux-ci/blob/2772e3b648ce1c1ae05f31e77732063c4103de09/konflux-ci/rbac/core/konflux-admin-user-actions.yaml
	KonfluxAdminUserActionsClusterRoleName = "konflux-admin-user-actions"
	// Default role binding name
	DefaultKonfluxAdminRoleBindingName = "user2-konflux-admin"
	// Default user name available after deploying upstream version of konflux-ci
	DefaultKonfluxCIUserName = "user2@konflux.dev"

	ImageControllerE2ETestNamesapcePrefix = "image-controller-e2e"
	BuildServiceE2ETestNamesapcePrefix    = "build-e2e"

	ComponentNamespacePullSecretName = "components-namespace-pull"

	SampleTestRepoName = "testrepo"

	// PipleineRun related constants
	PipelineRunPollingInterval = 20 * time.Second
)

var (
	githubOrg           = utils.GetEnv(GITHUB_E2E_ORGANIZATION_ENV, DefaultGithubOrg)
	GitLabProjectIdsMap = map[string]string{"hacbs-test-project-integration": "56586709", "devfile-sample-hello-world": "60038001", "build-nudge-parent": "62134305", "build-nudge-child": "62134341"}
)

func GetGitLabProjectId(repoName string) string {
	for name, id := range GitLabProjectIdsMap {
		if name == repoName {
			return id
		}
	}
	return ""
}
