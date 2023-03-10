/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package github

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v45/github"
	"github.com/redhat-appstudio/build-service/pkg/boerrors"
)

// Allow mocking for tests
var CreatePaCPullRequest func(g *GithubClient, d *PaCPullRequestData) (string, error) = ensurePaCPullRequest
var UndoPaCPullRequest func(g *GithubClient, d *PaCPullRequestData) (string, error) = undoPaCPullRequest
var SetupPaCWebhook func(g *GithubClient, webhookUrl, webhookSecret, owner, repository string) error = setupPaCWebhook
var DeletePaCWebhook func(g *GithubClient, webhookUrl, owner, repository string) error = deletePaCWebhook
var IsAppInstalledIntoRepository func(g *GithubClient, owner, repository string) (bool, error) = isAppInstalledIntoRepository
var GetDefaultBranch func(*GithubClient, string, string) (string, error) = getDefaultBranch

const (
	// Allowed values are 'json' and 'form' according to the doc: https://docs.github.com/en/rest/webhooks/repos#create-a-repository-webhook
	webhookContentType = "json"
)

var (
	appStudioPaCWebhookEvents = [...]string{"pull_request", "push", "issue_comment", "commit_comment"}
)

type File struct {
	FullPath string
	Content  []byte
}

type PaCPullRequestData struct {
	Owner         string
	Repository    string
	CommitMessage string
	Branch        string
	BaseBranch    string
	PRTitle       string
	PRText        string
	AuthorName    string
	AuthorEmail   string
	Files         []File
}

// ensurePaCPullRequest creates a new pull request or updates existing (if needed) and returns its web URL.
// If there is no error and web URL is empty, it means that the PR is not needed (main branch is up to date).
func ensurePaCPullRequest(ghclient *GithubClient, d *PaCPullRequestData) (string, error) {
	// Fallback to the default branch if base branch is not set
	if d.BaseBranch == "" {
		baseBranch, err := ghclient.getDefaultBranch(d.Owner, d.Repository)
		if err != nil {
			return "", err
		}
		d.BaseBranch = baseBranch
	}

	// Check if Pipelines as Code configuration up to date in the main branch
	upToDate, err := ghclient.filesUpToDate(d.Owner, d.Repository, d.BaseBranch, d.Files)
	if err != nil {
		return "", err
	}
	if upToDate {
		// Nothing to do, the configuration is alredy in the main branch of the repository
		return "", nil
	}

	// Check if branch with a proposal exists
	branchExists, err := ghclient.referenceExist(d.Owner, d.Repository, d.Branch)
	if err != nil {
		return "", err
	}

	if branchExists {
		upToDate, err := ghclient.filesUpToDate(d.Owner, d.Repository, d.Branch, d.Files)
		if err != nil {
			return "", err
		}
		if !upToDate {
			// Update branch
			branchRef, err := ghclient.getReference(d.Owner, d.Repository, d.Branch)
			if err != nil {
				return "", err
			}

			err = ghclient.addCommitToBranch(d.Owner, d.Repository, d.AuthorName, d.AuthorEmail, d.CommitMessage, d.Files, branchRef)
			if err != nil {
				return "", err
			}
		}

		pr, err := ghclient.findPullRequestByBranchesWithinRepository(d.Owner, d.Repository, d.Branch, d.BaseBranch)
		if err != nil {
			return "", err
		}
		if pr != nil {
			return *pr.HTMLURL, nil
		}

		prUrl, err := ghclient.createPullRequestWithinRepository(d.Owner, d.Repository, d.Branch, d.BaseBranch, d.PRTitle, d.PRText)
		if err != nil {
			if strings.Contains(err.Error(), "No commits between") {
				// This could happen when a PR was created and merged, but PR branch was not deleted. Then main was updated.
				// Current branch has correct configuration, but it's not possible to create a PR,
				// because current branch reference is included into main branch.
				if err := ghclient.deleteReference(d.Owner, d.Repository, d.Branch); err != nil {
					return "", err
				}
				return ensurePaCPullRequest(ghclient, d)
			}
		}
		return prUrl, nil

	} else {
		// Create branch, commit and pull request
		branchRef, err := ghclient.createReference(d.Owner, d.Repository, d.Branch, d.BaseBranch)
		if err != nil {
			return "", err
		}

		err = ghclient.addCommitToBranch(d.Owner, d.Repository, d.AuthorName, d.AuthorEmail, d.CommitMessage, d.Files, branchRef)
		if err != nil {
			return "", err
		}

		return ghclient.createPullRequestWithinRepository(d.Owner, d.Repository, d.Branch, d.BaseBranch, d.PRTitle, d.PRText)
	}
}

// undoPaCPullRequest creates a new pull request to remove PaC configuration for the component.
// Returns the pull request web URL.
// If there is no error and web URL is empty, it means that the PR is not needed (PaC configuraton has already been deleted).
func undoPaCPullRequest(ghclient *GithubClient, d *PaCPullRequestData) (string, error) {
	// Fallback to the default branch if base branch is not set
	if d.BaseBranch == "" {
		baseBranch, err := ghclient.getDefaultBranch(d.Owner, d.Repository)
		if err != nil {
			return "", err
		}
		d.BaseBranch = baseBranch
	}

	files, err := ghclient.filesExistInDirectory(d.Owner, d.Repository, d.BaseBranch, ".tekton", d.Files)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		// Nothing to prune
		return "", nil
	}

	// Need to create PR that deletes PaC configuration of the component

	// Check if branch exists
	branchExists, err := ghclient.referenceExist(d.Owner, d.Repository, d.Branch)
	if err != nil {
		return "", err
	}
	if branchExists {
		if err := ghclient.deleteReference(d.Owner, d.Repository, d.Branch); err != nil {
			return "", err
		}
	}

	// Create branch, commit and pull request
	branchRef, err := ghclient.createReference(d.Owner, d.Repository, d.Branch, d.BaseBranch)
	if err != nil {
		return "", err
	}

	err = ghclient.addDeleteCommitToBranch(d.Owner, d.Repository, d.AuthorName, d.AuthorEmail, d.CommitMessage, d.Files, branchRef)
	if err != nil {
		return "", err
	}

	return ghclient.createPullRequestWithinRepository(d.Owner, d.Repository, d.Branch, d.BaseBranch, d.PRTitle, d.PRText)
}

// SetupPaCWebhook creates or updates Pipelines as Code webhook configuration
func setupPaCWebhook(ghclient *GithubClient, webhookUrl, webhookSecret, owner, repository string) error {
	existingWebhook, err := ghclient.getWebhookByTargetUrl(owner, repository, webhookUrl)
	if err != nil {
		return err
	}

	defaultWebhook := getDefaultWebhookConfig(webhookUrl, webhookSecret)

	if existingWebhook == nil {
		// Webhook does not exist
		_, err = ghclient.createWebhook(owner, repository, defaultWebhook)
		return err
	}

	// Webhook exists
	// Need to always update the webhook in order to make sure that the webhook secret is up to date
	// (it is not possible to read existing webhook secret)
	existingWebhook.Config["secret"] = webhookSecret
	// It doesn't make sense to check target URL as it is used as webhook ID
	if existingWebhook.Config["content_type"] != webhookContentType {
		existingWebhook.Config["content_type"] = webhookContentType
	}
	if existingWebhook.Config["insecure_ssl"] != "1" {
		existingWebhook.Config["insecure_ssl"] = "1"
	}

	for _, requiredWebhookEvent := range appStudioPaCWebhookEvents {
		requiredEventFound := false
		for _, existingWebhookEvent := range existingWebhook.Events {
			if existingWebhookEvent == requiredWebhookEvent {
				requiredEventFound = true
				break
			}
		}
		if !requiredEventFound {
			existingWebhook.Events = append(existingWebhook.Events, requiredWebhookEvent)
		}
	}

	if *existingWebhook.Active != *defaultWebhook.Active {
		existingWebhook.Active = defaultWebhook.Active
	}

	_, err = ghclient.updateWebhook(owner, repository, existingWebhook)
	return err
}

func getDefaultWebhookConfig(webhookUrl, webhookSecret string) *github.Hook {
	return &github.Hook{
		Events: appStudioPaCWebhookEvents[:],
		Config: map[string]interface{}{
			"url":          webhookUrl,
			"content_type": webhookContentType,
			"secret":       webhookSecret,
			"insecure_ssl": "1", // TODO make this field configurable and set defaults to 0
		},
		Active: github.Bool(true),
	}
}

func deletePaCWebhook(ghclient *GithubClient, webhookUrl string, owner string, repository string) error {
	existingWebhook, err := ghclient.getWebhookByTargetUrl(owner, repository, webhookUrl)
	if err != nil {
		return err
	}
	if existingWebhook == nil {
		// Webhook doesn't exist, nothing to do
		return nil
	}

	return ghclient.deleteWebhook(owner, repository, *existingWebhook.ID)
}

func isAppInstalledIntoRepository(ghclient *GithubClient, owner, repository string) (bool, error) {
	return ghclient.isAppInstalledIntoRepository(owner, repository)
}

// RefineGitHostingServiceError generates expected permanent error from GitHub response.
// If no one is detected, the original error will be returned.
// RefineGitHostingServiceError should be called just after every GitHub API call.
func RefineGitHostingServiceError(response *http.Response, originErr error) error {
	// go-github APIs do not return a http.Response object if the error is not related to an HTTP request.
	if response == nil {
		return originErr
	}
	if _, ok := originErr.(*github.RateLimitError); ok {
		return boerrors.NewBuildOpError(boerrors.EGitHubReachRateLimit, originErr)
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		// Client's access token can't be recognized by GitHub.
		return boerrors.NewBuildOpError(boerrors.EGitHubTokenUnauthorized, originErr)
	case http.StatusNotFound:
		// No expected resource is found due to insufficient scope set to the client's access token.
		scopes := response.Header["X-Oauth-Scopes"]
		err := boerrors.NewBuildOpError(boerrors.EGitHubNoResourceToOperateOn, originErr)
		if len(scopes) == 0 {
			err.ExtraInfo = "No scope is found from response header. Check it from GitHub settings."
		} else {
			err.ExtraInfo = fmt.Sprintf("Scopes set to access token: %s", strings.Join(scopes, ", "))
		}
		return err
	default:
		return originErr
	}
}

func getDefaultBranch(client *GithubClient, owner string, repository string) (string, error) {
	return client.getDefaultBranch(owner, repository)
}
