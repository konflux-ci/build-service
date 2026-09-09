package common

import (
	"fmt"

	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/forgejo"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/git"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/github"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/gitlab"
	kubeCl "github.com/konflux-ci/build-service/e2e-tests/pkg/clients/kubernetes"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/constants"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/utils"
)

// Create the struct for kubernetes and github clients.
type SuiteController struct {
	*kubeCl.CustomClient
	Git     git.Client
	Github  *github.Github
	Gitlab  *gitlab.GitlabClient
	Forgejo *forgejo.ForgejoClient
}

func NewSuiteController(kubeC *kubeCl.CustomClient) (*SuiteController, error) {
	gh, err := github.NewGithubClient(utils.GetEnv(constants.GITHUB_TOKEN_ENV, ""), utils.GetEnv(constants.GITHUB_E2E_ORGANIZATION_ENV, constants.DefaultGithubOrg))
	if err != nil {
		return nil, err
	}

	// Initialize gitlab client
	var gl *gitlab.GitlabClient
	groupId := utils.GetEnv("GITLAB_GROUP_ID", constants.DefaultGilabGroupId)
	gitlabToken := utils.GetEnv(constants.GITLAB_BOT_TOKEN_ENV, "")
	if gitlabToken != "" {
		gl, err = gitlab.NewGitlabClient(gitlabToken, utils.GetEnv(constants.GITLAB_API_URL_ENV, constants.DefaultGitLabAPIURL), groupId)
		if err != nil {
			return nil, fmt.Errorf("failed to create gitlab client: %w", err)
		}
	}

	// Initialize Forgejo client (for Codeberg)
	var fj *forgejo.ForgejoClient
	forgejoToken := utils.GetEnv(constants.CODEBERG_BOT_TOKEN_ENV, "")
	if forgejoToken != "" {
		fj, err = forgejo.NewForgejoClient(
			forgejoToken,
			utils.GetEnv(constants.CODEBERG_API_URL_ENV, constants.DefaultCodebergAPIURL),
			utils.GetEnv(constants.CODEBERG_QE_ORG_ENV, constants.DefaultCodebergQEOrg),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize Forgejo/Codeberg client: %w", err)
		}
	}

	return &SuiteController{
		CustomClient: kubeC,
		Github:       gh,
		Gitlab:       gl,
		Forgejo:      fj,
	}, nil
}
