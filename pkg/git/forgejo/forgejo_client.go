/*
Copyright 2026 Red Hat, Inc.

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

package forgejo

import (
	"fmt"
	"strings"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	"github.com/konflux-ci/build-service/pkg/common"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
)

const (
	webhookContentType = "json"
)

var (
	// PaC webhook events required for Forgejo
	appStudioPaCWebhookEvents = []string{"pull_request", "push", "issue_comment", "commit_comment"}
)

// NewForgejoClient can be mocked in tests.
var NewForgejoClient func(accessToken, baseUrl string) (*ForgejoClient, error) = newForgejoClient
var NewForgejoClientWithBasicAuth func(username, password, baseUrl string) (*ForgejoClient, error) = newForgejoClientWithBasicAuth

var _ gp.GitProviderClient = (*ForgejoClient)(nil)

type ForgejoClient struct {
	client *forgejo.Client
}

// EnsurePaCMergeRequest creates or updates existing (if needed) Pipelines as Code configuration proposal merge request.
// Returns the merge request web URL.
// If there is no error and web URL is empty, it means that the merge request is not needed (main branch is up to date).
func (f *ForgejoClient) EnsurePaCMergeRequest(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return "", err
	}

	// Determine base branch
	if d.BaseBranchName == "" {
		baseBranch, err := f.getDefaultBranchWithChecks(owner, repository)
		if err != nil {
			return "", err
		}
		d.BaseBranchName = baseBranch
	} else {
		exists, err := f.branchExist(owner, repository, d.BaseBranchName)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", boerrors.NewBuildOpError(boerrors.EForgejoBranchDoesntExist, fmt.Errorf("base branch '%s' does not exist", d.BaseBranchName))
		}
	}

	// Check if files are already up to date in base branch
	filesUpToDate, err := f.filesUpToDate(owner, repository, d.BaseBranchName, d.Files)
	if err != nil {
		return "", err
	}
	if filesUpToDate {
		// Configuration is already in the base branch
		return "", nil
	}

	// Check if PR branch exists
	prBranchExists, err := f.branchExist(owner, repository, d.BranchName)
	if err != nil {
		return "", err
	}

	if prBranchExists {
		// Branch exists, check if files are up to date
		branchFilesUpToDate, err := f.filesUpToDate(owner, repository, d.BranchName, d.Files)
		if err != nil {
			return "", err
		}
		if !branchFilesUpToDate {
			// Update files in the branch
			err := f.commitFilesIntoBranch(owner, repository, d.BranchName, d.CommitMessage, d.AuthorName, d.AuthorEmail, d.SignedOff, d.Files)
			if err != nil {
				return "", err
			}
		}

		// Check if PR already exists
		pr, err := f.findPullRequestByBranches(owner, repository, d.BranchName, d.BaseBranchName)
		if err != nil {
			return "", err
		}
		if pr != nil {
			// PR already exists
			return pr.HTMLURL, nil
		}

		// Check if there's a diff between branches
		diffExists, err := f.diffNotEmpty(owner, repository, d.BranchName, d.BaseBranchName)
		if err != nil {
			return "", err
		}
		if !diffExists {
			// No diff - delete stale branch and recurse
			if _, err := f.deleteBranch(owner, repository, d.BranchName); err != nil {
				return "", err
			}
			return f.EnsurePaCMergeRequest(repoUrl, d)
		}

		// Create new PR
		return f.createPullRequestWithinRepository(owner, repository, d.BranchName, d.BaseBranchName, d.Title, d.Text)
	} else {
		// Branch doesn't exist - create it
		_, err = f.createBranch(owner, repository, d.BranchName, d.BaseBranchName)
		if err != nil {
			return "", err
		}

		// Commit files to the new branch
		err = f.commitFilesIntoBranch(owner, repository, d.BranchName, d.CommitMessage, d.AuthorName, d.AuthorEmail, d.SignedOff, d.Files)
		if err != nil {
			return "", err
		}

		// Create PR
		return f.createPullRequestWithinRepository(owner, repository, d.BranchName, d.BaseBranchName, d.Title, d.Text)
	}
}

// UndoPaCMergeRequest creates a merge request that removes Pipelines as Code configuration from the repository.
func (f *ForgejoClient) UndoPaCMergeRequest(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return "", err
	}

	// Determine base branch
	if d.BaseBranchName == "" {
		baseBranch, err := f.getDefaultBranchWithChecks(owner, repository)
		if err != nil {
			return "", err
		}
		d.BaseBranchName = baseBranch
	} else {
		exists, err := f.branchExist(owner, repository, d.BaseBranchName)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", boerrors.NewBuildOpError(boerrors.EForgejoBranchDoesntExist, fmt.Errorf("base branch '%s' does not exist", d.BaseBranchName))
		}
	}

	// Check if any files to delete exist in base branch
	hasFilesToDelete := false
	for _, file := range d.Files {
		exists, _, err := f.fileExist(owner, repository, d.BaseBranchName, file.FullPath)
		if err != nil {
			return "", err
		}
		if exists {
			hasFilesToDelete = true
			break
		}
	}

	if !hasFilesToDelete {
		// Nothing to delete, configuration already removed
		return "", nil
	}

	// Delete old branch if it exists
	branchExists, err := f.branchExist(owner, repository, d.BranchName)
	if err != nil {
		return "", err
	}
	if branchExists {
		if _, err := f.deleteBranch(owner, repository, d.BranchName); err != nil {
			return "", err
		}
	}

	// Create new branch
	_, err = f.createBranch(owner, repository, d.BranchName, d.BaseBranchName)
	if err != nil {
		return "", err
	}

	// Commit file deletions
	err = f.commitDeletesIntoBranch(owner, repository, d.BranchName, d.CommitMessage, d.AuthorName, d.AuthorEmail, d.SignedOff, d.Files)
	if err != nil {
		return "", err
	}

	// Create PR
	return f.createPullRequestWithinRepository(owner, repository, d.BranchName, d.BaseBranchName, d.Title, d.Text)
}

// FindUnmergedPaCMergeRequest finds an existing unmerged PaC configuration merge request.
func (f *ForgejoClient) FindUnmergedPaCMergeRequest(repoUrl string, d *gp.MergeRequestData) (*gp.MergeRequest, error) {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return nil, err
	}

	// Determine base branch if not specified
	baseBranch := d.BaseBranchName
	if baseBranch == "" {
		baseBranch, err = f.getDefaultBranchWithChecks(owner, repository)
		if err != nil {
			return nil, err
		}
	}

	// Find PR by branches
	pr, err := f.findPullRequestByBranches(owner, repository, d.BranchName, baseBranch)
	if err != nil {
		return nil, err
	}

	if pr == nil {
		return nil, nil
	}

	// Convert Forgejo PR to MergeRequest
	return &gp.MergeRequest{
		Id:        pr.ID,
		CreatedAt: pr.Created,
		WebUrl:    pr.HTMLURL,
		Title:     pr.Title,
	}, nil
}

// SetupPaCWebhook creates a webhook for Pipelines as Code in the repository.
func (f *ForgejoClient) SetupPaCWebhook(repoUrl string, webhookUrl string, webhookSecret string) error {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return err
	}

	// Check if webhook already exists
	existingWebhook, err := f.getWebhookByTargetUrl(owner, repository, webhookUrl)
	if err != nil {
		return err
	}

	insecureSSL := gp.IsInsecureSSL()

	if existingWebhook == nil {
		// Create new webhook
		hookOpt := &forgejo.CreateHookOption{
			Type: "forgejo",
			Config: map[string]string{
				"url":          webhookUrl,
				"content_type": webhookContentType,
				"secret":       webhookSecret,
			},
			Events:       appStudioPaCWebhookEvents,
			Active:       true,
			BranchFilter: "*",
		}

		if insecureSSL {
			hookOpt.Config["insecure_ssl"] = "1"
		} else {
			hookOpt.Config["insecure_ssl"] = "0"
		}

		_, err = f.createWebhook(owner, repository, hookOpt)
		return err
	}

	// Update existing webhook to ensure it has correct configuration
	updateOpt := &forgejo.EditHookOption{
		Config: map[string]string{
			"url":          webhookUrl,
			"content_type": webhookContentType,
			"secret":       webhookSecret,
		},
		Events:       appStudioPaCWebhookEvents,
		Active:       forgejo.OptionalBool(true),
		BranchFilter: "*",
	}

	if insecureSSL {
		updateOpt.Config["insecure_ssl"] = "1"
	} else {
		updateOpt.Config["insecure_ssl"] = "0"
	}

	_, err = f.updateWebhook(owner, repository, existingWebhook.ID, updateOpt)
	return err
}

// DeletePaCWebhook deletes the Pipelines as Code webhook from the repository.
func (f *ForgejoClient) DeletePaCWebhook(repoUrl string, webhookUrl string) error {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return err
	}

	// Find webhook by URL
	existingWebhook, err := f.getWebhookByTargetUrl(owner, repository, webhookUrl)
	if err != nil {
		return err
	}

	if existingWebhook == nil {
		// Webhook doesn't exist, nothing to delete
		return nil
	}

	// Delete webhook
	return f.deleteWebhook(owner, repository, existingWebhook.ID)
}

// GetDefaultBranchWithChecks returns the default branch of the repository with additional checks.
func (f *ForgejoClient) GetDefaultBranchWithChecks(repoUrl string) (string, error) {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return "", err
	}

	return f.getDefaultBranchWithChecks(owner, repository)
}

// DeleteBranch deletes a branch from the repository.
func (f *ForgejoClient) DeleteBranch(repoUrl string, branchName string) (bool, error) {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return false, err
	}

	return f.deleteBranch(owner, repository, branchName)
}

// GetBranchSha returns the SHA of the latest commit on the specified branch.
func (f *ForgejoClient) GetBranchSha(repoUrl string, branchName string) (string, error) {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return "", err
	}

	branch, resp, err := f.getBranch(owner, repository, branchName)
	if err != nil {
		return "", refineGitHostingServiceError(resp.Response, err)
	}
	if branch == nil {
		return "", fmt.Errorf("branch '%s' not found", branchName)
	}

	return branch.Commit.ID, nil
}

// GetBrowseRepositoryAtShaLink returns a web URL to view the repository at a specific commit SHA.
func (f *ForgejoClient) GetBrowseRepositoryAtShaLink(repoUrl string, sha string) string {
	// Forgejo repository URL format: https://forgejo.example.com/owner/repo/commit/sha
	baseUrl, err := GetBaseUrl(repoUrl)
	if err != nil {
		return ""
	}

	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return ""
	}

	baseUrl = strings.TrimSuffix(baseUrl, "/")
	return fmt.Sprintf("%s/%s/%s/commit/%s", baseUrl, owner, repository, sha)
}

// DownloadFileContent downloads the content of a file from the repository.
func (f *ForgejoClient) DownloadFileContent(repoUrl, branchName, filePath string) ([]byte, error) {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return nil, err
	}

	return f.downloadFileContent(owner, repository, branchName, filePath)
}

// IsFileExist checks if a file exists in the repository at the specified branch.
func (f *ForgejoClient) IsFileExist(repoUrl, branchName, filePath string) (bool, error) {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return false, err
	}

	exists, _, err := f.fileExist(owner, repository, branchName, filePath)
	return exists, err
}

// IsRepositoryPublic checks if the repository is publicly accessible.
func (f *ForgejoClient) IsRepositoryPublic(repoUrl string) (bool, error) {
	owner, repository, err := getOwnerAndRepoFromUrl(repoUrl)
	if err != nil {
		return false, err
	}

	return f.isRepositoryPublic(owner, repository)
}

// GetConfiguredGitAppName returns the configured Git App name.
// Forgejo does not support GitHub-style Apps, so this returns an error.
func (f *ForgejoClient) GetConfiguredGitAppName() (string, string, error) {
	return "", "", boerrors.NewBuildOpError(boerrors.EForgejoGitAppNotSupported,
		fmt.Errorf("forgejo does not support GitHub-style applications"))
}

// GetAppUserId returns the user ID of the configured Git App.
// Forgejo does not support GitHub-style Apps, so this returns an error.
func (f *ForgejoClient) GetAppUserId(userName string) (int64, error) {
	return 0, boerrors.NewBuildOpError(boerrors.EForgejoGitAppNotSupported,
		fmt.Errorf("forgejo does not support GitHub-style applications"))
}

// newForgejoClient creates a new Forgejo client with token authentication
func newForgejoClient(accessToken, baseUrl string) (*ForgejoClient, error) {
	client, err := forgejo.NewClient(
		baseUrl,
		forgejo.SetToken(accessToken),
		forgejo.SetUserAgent(common.BuildServiceUserAgent),
	)
	if err != nil {
		return nil, err
	}
	return &ForgejoClient{client: client}, nil
}

// newForgejoClientWithBasicAuth creates a new Forgejo client with basic authentication
func newForgejoClientWithBasicAuth(username, password, baseUrl string) (*ForgejoClient, error) {
	client, err := forgejo.NewClient(
		baseUrl,
		forgejo.SetBasicAuth(username, password),
		forgejo.SetUserAgent(common.BuildServiceUserAgent),
	)
	if err != nil {
		return nil, err
	}
	return &ForgejoClient{client: client}, nil
}
