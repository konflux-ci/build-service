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
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
)

type FailedToParseUrlError struct {
	url string
	err string
}

func (e FailedToParseUrlError) Error() string {
	return fmt.Sprintf("Failed to parse url: %s, error: %s", e.url, e.err)
}

type MissingSchemaError struct {
	url string
}

func (e MissingSchemaError) Error() string {
	return fmt.Sprintf("Failed to detect schema in url %s", e.url)
}

type MissingHostError struct {
	url string
}

func (e MissingHostError) Error() string {
	return fmt.Sprintf("Failed to detect host in url %s", e.url)
}

// getOwnerAndRepoFromUrl extracts owner and repository name from repository URL.
// Example: https://forgejo.example.com/owner/repository -> owner, repository
func getOwnerAndRepoFromUrl(repoUrl string) (owner string, repository string, err error) {
	parsedUrl, err := url.Parse(strings.TrimSuffix(repoUrl, ".git"))
	if err != nil {
		return "", "", err
	}

	pathParts := strings.Split(strings.TrimPrefix(parsedUrl.Path, "/"), "/")
	if len(pathParts) < 2 {
		return "", "", fmt.Errorf("invalid repository URL format: %s", repoUrl)
	}

	owner = pathParts[0]
	repository = pathParts[1]
	return owner, repository, nil
}

// GetBaseUrl extracts the base URL from repository URL.
// Example: https://forgejo.example.com/owner/repository -> https://forgejo.example.com/
func GetBaseUrl(repoUrl string) (string, error) {
	parsedUrl, err := url.Parse(repoUrl)
	if err != nil {
		return "", FailedToParseUrlError{url: repoUrl, err: err.Error()}
	}

	if parsedUrl.Scheme == "" {
		return "", MissingSchemaError{repoUrl}
	}

	if parsedUrl.Host == "" {
		return "", MissingHostError{repoUrl}
	}

	// The forgejo client library expects the base url to have a trailing slash
	return fmt.Sprintf("%s://%s/", parsedUrl.Scheme, parsedUrl.Host), nil
}

// refineGitHostingServiceError generates expected permanent error from Forgejo response.
// If no one is detected, the original error will be returned.
// refineGitHostingServiceError should be called just after every Forgejo API call.
func refineGitHostingServiceError(response *http.Response, originErr error) error {
	// Forgejo SDK APIs do not return a http.Response object if the error is not related to an HTTP request.
	if response == nil {
		return originErr
	}

	switch response.StatusCode {
	case http.StatusUnauthorized:
		return boerrors.NewBuildOpError(boerrors.EForgejoTokenUnauthorized, originErr)
	case http.StatusForbidden:
		return boerrors.NewBuildOpError(boerrors.EForgejoTokenInsufficientScope, originErr)
	case http.StatusNotFound:
		return boerrors.NewBuildOpError(boerrors.EForgejoRepositoryNotFound, originErr)
	default:
		return originErr
	}
}

func (f *ForgejoClient) getBranch(owner, repository, branchName string) (*forgejo.Branch, *forgejo.Response, error) {
	branch, resp, err := f.client.GetRepoBranch(owner, repository, branchName)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, resp, nil
		}
		return nil, resp, err
	}
	return branch, resp, nil
}

func (f *ForgejoClient) branchExist(owner, repository, branchName string) (bool, error) {
	_, resp, err := f.client.GetRepoBranch(owner, repository, branchName)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (f *ForgejoClient) createBranch(owner, repository, branchName, baseBranchName string) (*forgejo.Branch, error) {
	opts := forgejo.CreateBranchOption{
		BranchName: branchName,
	}

	// Get the base branch to get the commit SHA
	baseBranch, resp, err := f.getBranch(owner, repository, baseBranchName)
	if err != nil {
		return nil, refineGitHostingServiceError(resp.Response, err)
	}
	if baseBranch == nil {
		return nil, fmt.Errorf("base branch '%s' not found", baseBranchName)
	}

	opts.OldBranchName = baseBranchName

	branch, resp, err := f.client.CreateBranch(owner, repository, opts)
	return branch, refineGitHostingServiceError(resp.Response, err)
}

func (f *ForgejoClient) deleteBranch(owner, repository, branchName string) (bool, error) {
	deleted, resp, err := f.client.DeleteRepoBranch(owner, repository, branchName)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			// The given branch doesn't exist
			return false, nil
		}
		return false, refineGitHostingServiceError(resp.Response, err)
	}
	return deleted, nil
}

func (f *ForgejoClient) getDefaultBranch(owner, repository string) (string, error) {
	repo, resp, err := f.client.GetRepo(owner, repository)
	if err != nil {
		return "", refineGitHostingServiceError(resp.Response, err)
	}
	if repo == nil {
		return "", fmt.Errorf("repository info is empty in Forgejo API response")
	}
	return repo.DefaultBranch, nil
}

// downloadFileContent retrieves requested file.
// filePath must be the full path to the file.
func (f *ForgejoClient) downloadFileContent(owner, repository, branch, filePath string) ([]byte, error) {
	contents, resp, err := f.client.GetContents(owner, repository, branch, filePath)
	if err != nil {
		// Check if file not found
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, errors.New("not found")
		}
		return nil, refineGitHostingServiceError(resp.Response, err)
	}

	// Forgejo returns file content in the Content field
	if contents == nil || contents.Content == nil {
		return nil, errors.New("not found")
	}

	return []byte(*contents.Content), nil
}

// filesUpToDate checks if all given files have expected content in remote git repository.
func (f *ForgejoClient) filesUpToDate(owner, repository, branch string, files []gp.RepositoryFile) (bool, error) {
	for _, file := range files {
		remoteFileBytes, err := f.downloadFileContent(owner, repository, branch, file.FullPath)
		if err != nil {
			if err.Error() == "not found" {
				// File doesn't exist in the repository
				return false, nil
			}
			return false, err
		}

		if !bytes.Equal(file.Content, remoteFileBytes) {
			// File content differs
			return false, nil
		}
	}
	return true, nil
}

func (f *ForgejoClient) getDefaultBranchWithChecks(owner, repository string) (string, error) {
	defaultBranch, err := f.getDefaultBranch(owner, repository)
	if err != nil {
		return "", err
	}

	// Verify the branch exists
	exists, err := f.branchExist(owner, repository, defaultBranch)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("default branch '%s' does not exist", defaultBranch)
	}

	return defaultBranch, nil
}

// fileExist checks if a file exists in the repository at the given branch
// Returns: exists (bool), sha (string), error
func (f *ForgejoClient) fileExist(owner, repository, branch, filePath string) (bool, string, error) {
	contents, resp, err := f.client.GetContents(owner, repository, branch, filePath)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, "", nil
		}
		return false, "", refineGitHostingServiceError(resp.Response, err)
	}
	if contents == nil {
		return false, "", nil
	}
	return true, contents.SHA, nil
}

// isRepositoryPublic checks if the repository is public
func (f *ForgejoClient) isRepositoryPublic(owner, repository string) (bool, error) {
	repo, resp, err := f.client.GetRepo(owner, repository)
	if err != nil {
		return false, refineGitHostingServiceError(resp.Response, err)
	}
	if repo == nil {
		return false, fmt.Errorf("repository info is empty in Forgejo API response")
	}
	return !repo.Private, nil
}

// commitFilesIntoBranch creates a commit with the given files in the specified branch
func (f *ForgejoClient) commitFilesIntoBranch(owner, repository, branchName, commitMessage, authorName, authorEmail string, signedOff bool, files []gp.RepositoryFile) error {
	// Note: Forgejo API doesn't support multi-file commits in a single operation
	// Each file needs to be created/updated individually
	for _, file := range files {
		// Check if file exists to determine if we should create or update
		exists, sha, err := f.fileExist(owner, repository, branchName, file.FullPath)
		if err != nil {
			return err
		}

		author := forgejo.Identity{
			Name:  authorName,
			Email: authorEmail,
		}

		if exists {
			// Update existing file
			opts := forgejo.UpdateFileOptions{
				FileOptions: forgejo.FileOptions{
					Message:    commitMessage,
					BranchName: branchName,
					Author:     author,
					Committer:  author,
					Signoff:    signedOff,
				},
				SHA:     sha,
				Content: base64Encode(file.Content),
			}
			_, resp, err := f.client.UpdateFile(owner, repository, file.FullPath, opts)
			if err != nil {
				return refineGitHostingServiceError(resp.Response, err)
			}
		} else {
			// Create new file
			opts := forgejo.CreateFileOptions{
				FileOptions: forgejo.FileOptions{
					Message:    commitMessage,
					BranchName: branchName,
					Author:     author,
					Committer:  author,
					Signoff:    signedOff,
				},
				Content: base64Encode(file.Content),
			}
			_, resp, err := f.client.CreateFile(owner, repository, file.FullPath, opts)
			if err != nil {
				return refineGitHostingServiceError(resp.Response, err)
			}
		}
	}

	return nil
}

// commitDeletesIntoBranch creates a commit that deletes the given files from the specified branch
func (f *ForgejoClient) commitDeletesIntoBranch(owner, repository, branchName, commitMessage, authorName, authorEmail string, signedOff bool, files []gp.RepositoryFile) error {
	// Delete each file individually
	for _, file := range files {
		// Get the file's SHA before deleting
		exists, sha, err := f.fileExist(owner, repository, branchName, file.FullPath)
		if err != nil {
			return fmt.Errorf("failed to check if file %s exists in %s/%s branch %s: %w", file.FullPath, owner, repository, branchName, err)
		}
		if !exists {
			// File doesn't exist, skip deletion
			continue
		}

		author := forgejo.Identity{
			Name:  authorName,
			Email: authorEmail,
		}

		opts := forgejo.DeleteFileOptions{
			FileOptions: forgejo.FileOptions{
				Message:    commitMessage,
				BranchName: branchName,
				Author:     author,
				Committer:  author,
				Signoff:    signedOff,
			},
			SHA: sha,
		}

		resp, err := f.client.DeleteFile(owner, repository, file.FullPath, opts)
		if err != nil {
			// Ignore error if file doesn't exist
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				continue
			}
			return refineGitHostingServiceError(resp.Response, err)
		}
	}

	return nil
}

// findPullRequestByBranches searches for a PR within repository by head and base branches
func (f *ForgejoClient) findPullRequestByBranches(owner, repository, headBranch, baseBranch string) (*forgejo.PullRequest, error) {
	opts := forgejo.ListPullRequestsOptions{
		State: forgejo.StateOpen,
		ListOptions: forgejo.ListOptions{
			Page:     1,
			PageSize: 100,
		},
	}

	prs, resp, err := f.client.ListRepoPullRequests(owner, repository, opts)
	if err != nil {
		return nil, refineGitHostingServiceError(resp.Response, err)
	}

	// Filter by head and base branches
	var matchingPRs []*forgejo.PullRequest
	for _, pr := range prs {
		if pr.Head != nil && pr.Base != nil {
			if pr.Head.Ref == headBranch && pr.Base.Ref == baseBranch {
				matchingPRs = append(matchingPRs, pr)
			}
		}
	}

	switch len(matchingPRs) {
	case 0:
		return nil, nil
	case 1:
		return matchingPRs[0], nil
	default:
		return nil, fmt.Errorf("found %d pull requests for head=%s base=%s, expected 0 or 1", len(matchingPRs), headBranch, baseBranch)
	}
}

// createPullRequestWithinRepository creates a new pull request in the repository
func (f *ForgejoClient) createPullRequestWithinRepository(owner, repository, headBranch, baseBranch, title, body string) (string, error) {
	opts := forgejo.CreatePullRequestOption{
		Head:  headBranch,
		Base:  baseBranch,
		Title: title,
		Body:  body,
	}

	pr, resp, err := f.client.CreatePullRequest(owner, repository, opts)
	if err != nil {
		return "", refineGitHostingServiceError(resp.Response, err)
	}

	return pr.HTMLURL, nil
}

// diffNotEmpty checks if there are differences between two branches
func (f *ForgejoClient) diffNotEmpty(owner, repository, headBranch, baseBranch string) (bool, error) {
	// Get branch info for both branches
	headBranchInfo, resp, err := f.client.GetRepoBranch(owner, repository, headBranch)
	if err != nil {
		return false, refineGitHostingServiceError(resp.Response, err)
	}

	baseBranchInfo, resp, err := f.client.GetRepoBranch(owner, repository, baseBranch)
	if err != nil {
		return false, refineGitHostingServiceError(resp.Response, err)
	}

	// If the commit SHAs are the same, there's no diff
	if headBranchInfo.Commit != nil && baseBranchInfo.Commit != nil {
		if headBranchInfo.Commit.ID == baseBranchInfo.Commit.ID {
			return false, nil
		}
	}

	// Otherwise there is a diff
	return true, nil
}

// getWebhookByTargetUrl returns webhook by its target URL or nil if it doesn't exist
func (f *ForgejoClient) getWebhookByTargetUrl(owner, repository, webhookTargetUrl string) (*forgejo.Hook, error) {
	opts := forgejo.ListHooksOptions{
		ListOptions: forgejo.ListOptions{
			Page:     1,
			PageSize: 100,
		},
	}

	webhooks, resp, err := f.client.ListRepoHooks(owner, repository, opts)
	if err != nil {
		return nil, refineGitHostingServiceError(resp.Response, err)
	}

	for _, webhook := range webhooks {
		if webhook.Config["url"] == webhookTargetUrl {
			return webhook, nil
		}
	}

	return nil, nil
}

// createWebhook creates a new webhook in the repository
func (f *ForgejoClient) createWebhook(owner, repository string, hook *forgejo.CreateHookOption) (*forgejo.Hook, error) {
	webhook, resp, err := f.client.CreateRepoHook(owner, repository, *hook)
	return webhook, refineGitHostingServiceError(resp.Response, err)
}

// updateWebhook updates an existing webhook
func (f *ForgejoClient) updateWebhook(owner, repository string, webhookID int64, hook *forgejo.EditHookOption) (*forgejo.Hook, error) {
	resp, err := f.client.EditRepoHook(owner, repository, webhookID, *hook)
	return nil, refineGitHostingServiceError(resp.Response, err)
}

// deleteWebhook deletes a webhook
func (f *ForgejoClient) deleteWebhook(owner, repository string, webhookID int64) error {
	resp, err := f.client.DeleteRepoHook(owner, repository, webhookID)
	return refineGitHostingServiceError(resp.Response, err)
}

// base64Encode encodes a byte slice to base64 string as required by Forgejo API
func base64Encode(content []byte) string {
	return base64.StdEncoding.EncodeToString(content)
}
