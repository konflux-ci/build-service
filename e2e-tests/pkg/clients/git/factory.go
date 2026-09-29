package git

import (
	"fmt"

	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/forgejo"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/github"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/gitlab"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/constants"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/utils"
)

// providerTokenEnvVars maps a git provider to the environment variable holding its access token.
var providerTokenEnvVars = map[GitProvider]string{
	GitHubProvider:  constants.GITHUB_TOKEN_ENV,
	GitLabProvider:  constants.GITLAB_BOT_TOKEN_ENV,
	ForgejoProvider: constants.CODEBERG_BOT_TOKEN_ENV,
}

// Clients holds a provider agnostic git client for every git provider configured in the test environment.
type Clients map[GitProvider]Client

// Get returns the client of the given git provider,
// or an error if the provider is not configured in the test environment.
func (c Clients) Get(provider GitProvider) (Client, error) {
	client, ok := c[provider]
	if !ok || client == nil {
		return nil, fmt.Errorf("no %s client configured, set %s to enable it", provider, providerTokenEnvVars[provider])
	}
	return client, nil
}

// NewClientsFromEnv creates a git client for each git provider configured in the environment.
// GitHub is always configured, GitLab and Forgejo clients are created only when their access token is given.
func NewClientsFromEnv() (Clients, error) {
	clients := Clients{}

	gh, err := github.NewGithubClient(
		utils.GetEnv(constants.GITHUB_TOKEN_ENV, ""),
		utils.GetEnv(constants.GITHUB_E2E_ORGANIZATION_ENV, constants.DefaultGithubOrg),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create github client: %w", err)
	}
	clients[GitHubProvider] = NewGitHubClient(gh)

	if gitlabToken := utils.GetEnv(constants.GITLAB_BOT_TOKEN_ENV, ""); gitlabToken != "" {
		gl, err := gitlab.NewGitlabClient(
			gitlabToken,
			utils.GetEnv(constants.GITLAB_API_URL_ENV, constants.DefaultGitLabAPIURL),
			utils.GetEnv("GITLAB_GROUP_ID", constants.DefaultGitlabGroupId),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create gitlab client: %w", err)
		}
		clients[GitLabProvider] = NewGitlabClient(gl)
	}

	// Forgejo is used to talk to Codeberg.
	if forgejoToken := utils.GetEnv(constants.CODEBERG_BOT_TOKEN_ENV, ""); forgejoToken != "" {
		fj, err := forgejo.NewForgejoClient(
			forgejoToken,
			utils.GetEnv(constants.CODEBERG_API_URL_ENV, constants.DefaultCodebergAPIURL),
			utils.GetEnv(constants.CODEBERG_QE_ORG_ENV, constants.DefaultCodebergQEOrg),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create forgejo/codeberg client: %w", err)
		}
		clients[ForgejoProvider] = NewForgejoClient(fj)
	}

	return clients, nil
}
