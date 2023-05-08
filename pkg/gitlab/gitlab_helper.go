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

package gitlab

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/redhat-appstudio/build-service/pkg/boerrors"
	"github.com/xanzy/go-gitlab"
)

// Allow mocking for tests
var EnsurePaCMergeRequest func(g *GitlabClient, d *PaCMergeRequestData) (string, error) = ensurePaCMergeRequest
var UndoPaCMergeRequest func(g *GitlabClient, d *PaCMergeRequestData) (string, error) = undoPaCMergeRequest
var SetupPaCWebhook func(g *GitlabClient, projectPath, webhookUrl, webhookSecret string) error = setupPaCWebhook
var DeletePaCWebhook func(g *GitlabClient, projectPath, webhookUrl string) error = deletePaCWebhook
var GetDefaultBranch func(*GitlabClient, string) (string, error) = getDefaultBranch
var FindUnmergedOnboardingMergeRequest func(*GitlabClient, string, string, string, string) (*gitlab.MergeRequest, error) = findUnmergedOnboardingMergeRequest
var DeleteBranch func(*GitlabClient, string, string) error = deleteBranch

type File struct {
	FullPath string
	Content  []byte
}

type PaCMergeRequestData struct {
	ProjectPath   string
	CommitMessage string
	Branch        string
	BaseBranch    string
	MrTitle       string
	MrText        string
	AuthorName    string
	AuthorEmail   string
	Files         []File
}

// ensurePaCMergeRequest creates a new merge request and returns its web URL
func ensurePaCMergeRequest(glclient *GitlabClient, d *PaCMergeRequestData) (string, error) {
	// Fallback to the default branch if base branch is not set
	if d.BaseBranch == "" {
		baseBranch, err := glclient.getDefaultBranch(d.ProjectPath)
		if err != nil {
			return "", err
		}
		d.BaseBranch = baseBranch
	}

	pacConfigurationUpToDate, err := glclient.filesUpToDate(d.ProjectPath, d.BaseBranch, d.Files)
	if err != nil {
		return "", err
	}
	if pacConfigurationUpToDate {
		// Nothing to do, the configuration is alredy in the main branch of the repository
		return "", nil
	}

	mrBranchExists, err := glclient.branchExist(d.ProjectPath, d.Branch)
	if err != nil {
		return "", err
	}

	if mrBranchExists {
		mrBranchUpToDate, err := glclient.filesUpToDate(d.ProjectPath, d.Branch, d.Files)
		if err != nil {
			return "", err
		}
		if !mrBranchUpToDate {
			err := glclient.commitFilesIntoBranch(d.ProjectPath, d.Branch, d.CommitMessage, d.AuthorName, d.AuthorEmail, d.Files)
			if err != nil {
				return "", err
			}
		}

		mr, err := glclient.findMergeRequestByBranches(d.ProjectPath, d.Branch, d.BaseBranch)
		if err != nil {
			return "", err
		}
		if mr != nil {
			// Merge request already exists
			return mr.WebURL, nil
		}

		diffExists, err := glclient.diffNotEmpty(d.ProjectPath, d.Branch, d.BaseBranch)
		if err != nil {
			return "", err
		}
		if !diffExists {
			// This situation occurs if an MR was merged but the branch was not deleted and main is changed after the merge.
			// Despite the fact that there is actual diff between branches, git treats it as no diff,
			// because the branch is already "included" in main.
			if err := glclient.deleteBranch(d.ProjectPath, d.Branch); err != nil {
				return "", err
			}
			return ensurePaCMergeRequest(glclient, d)
		}

		return glclient.createMergeRequestWithinRepository(d.ProjectPath, d.Branch, d.BaseBranch, d.MrTitle, d.MrText)

	} else {

		// Need to create branch and MR with Pipelines as Code configuration
		err = glclient.createBranch(d.ProjectPath, d.Branch, d.BaseBranch)
		if err != nil {
			return "", err
		}

		err = glclient.commitFilesIntoBranch(d.ProjectPath, d.Branch, d.CommitMessage, d.AuthorName, d.AuthorEmail, d.Files)
		if err != nil {
			return "", err
		}

		return glclient.createMergeRequestWithinRepository(d.ProjectPath, d.Branch, d.BaseBranch, d.MrTitle, d.MrText)
	}
}

func undoPaCMergeRequest(glclient *GitlabClient, d *PaCMergeRequestData) (string, error) {
	// Fallback to the default branch if base branch is not set
	if d.BaseBranch == "" {
		baseBranch, err := glclient.getDefaultBranch(d.ProjectPath)
		if err != nil {
			return "", err
		}
		d.BaseBranch = baseBranch
	}

	files, err := glclient.filesExistInDirectory(d.ProjectPath, d.BaseBranch, ".tekton", d.Files)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		// Nothing to prune
		return "", nil
	}

	// Need to create MR that deletes PaC configuration of the component

	// Check if branch exists
	branchExists, err := glclient.branchExist(d.ProjectPath, d.Branch)
	if err != nil {
		return "", err
	}
	if branchExists {
		if err := glclient.deleteBranch(d.ProjectPath, d.Branch); err != nil {
			return "", err
		}
	}

	// Create branch, commit and pull request
	if err := glclient.createBranch(d.ProjectPath, d.Branch, d.BaseBranch); err != nil {
		return "", err
	}

	err = glclient.addDeleteCommitToBranch(d.ProjectPath, d.Branch, d.AuthorName, d.AuthorEmail, d.CommitMessage, files)
	if err != nil {
		return "", err
	}

	return glclient.createMergeRequestWithinRepository(d.ProjectPath, d.Branch, d.BaseBranch, d.MrTitle, d.MrText)
}

func setupPaCWebhook(glclient *GitlabClient, projectPath, webhookUrl, webhookSecret string) error {
	existingWebhook, err := glclient.getWebhookByTargetUrl(projectPath, webhookUrl)
	if err != nil {
		return err
	}

	if existingWebhook == nil {
		_, err = glclient.createPaCWebhook(projectPath, webhookUrl, webhookSecret)
		return err
	}

	_, err = glclient.updatePaCWebhook(projectPath, existingWebhook.ID, webhookUrl, webhookSecret)
	return err
}

func deletePaCWebhook(glclient *GitlabClient, projectPath, webhookUrl string) error {
	existingWebhook, err := glclient.getWebhookByTargetUrl(projectPath, webhookUrl)
	if err != nil {
		return err
	}

	if existingWebhook == nil {
		// Webhook doesn't exist, nothing to do
		return nil
	}

	return glclient.deleteWebhook(projectPath, existingWebhook.ID)
}

// RefineGitHostingServiceError generates expected permanent error from GitHub response.
// If no one is detected, the original error will be returned.
// RefineGitHostingServiceError should be called just after every GitHub API call.
func RefineGitHostingServiceError(response *http.Response, originErr error) error {
	// go-gitlab APIs do not return a http.Response object if the error is not related to an HTTP request.
	if response == nil {
		return originErr
	}
	switch response.StatusCode {
	case 401:
		return boerrors.NewBuildOpError(boerrors.EGitLabTokenUnauthorized, originErr)
	case 403:
		return boerrors.NewBuildOpError(boerrors.EGitLabTokenInsufficientScope, originErr)
	default:
		return originErr
	}
}

func getDefaultBranch(client *GitlabClient, projectPath string) (string, error) {
	return client.getDefaultBranch(projectPath)
}

// GetBranchSHA returns SHA of the top commit in the given branch
func GetBranchSHA(client *GitlabClient, projectPath, branchName string) (string, error) {
	branch, err := client.getBranch(projectPath, branchName)
	if err != nil {
		return "", err
	}
	if branch.Commit == nil {
		return "", fmt.Errorf("unexpected response while getting branch top commit SHA")
	}
	sha := branch.Commit.ID
	return sha, nil
}

// findUnmergedOnboardingMergeRequest finds out the unmerged merge request that is opened during the component onboarding
// An onboarding merge request fulfills all the following criteria:
// 1) opened based on the base branch which is determined by the Revision or is the default branch of component repository
// 2) opened by a specific author
// 3) opened from source branch appstudio-{component.Name}
// If no onboarding merge request is found, nil is returned.
func findUnmergedOnboardingMergeRequest(
	glclient *GitlabClient, projectPath, sourceBranch, baseBranch, authorName string) (*gitlab.MergeRequest, error) {
	opts := &gitlab.ListProjectMergeRequestsOptions{
		State:          gitlab.String("opened"),
		AuthorUsername: gitlab.String(authorName),
		SourceBranch:   gitlab.String(sourceBranch),
		TargetBranch:   gitlab.String(baseBranch),
	}
	mrs, resp, err := glclient.client.MergeRequests.ListProjectMergeRequests(projectPath, opts)
	if err != nil {
		return nil, RefineGitHostingServiceError(resp.Response, err)
	}
	if len(mrs) == 0 {
		return nil, nil
	}
	return mrs[0], nil
}

func deleteBranch(glclient *GitlabClient, projectPath, branch string) error {
	if resp, err := glclient.client.Branches.DeleteBranch(projectPath, branch); err != nil {
		return RefineGitHostingServiceError(resp.Response, err)
	}
	return nil
}

func GetBrowseRepositoryAtShaLink(repoUrl, sha string) string {
	repoUrl = strings.TrimSuffix(repoUrl, ".git")
	gitSourceUrlParts := strings.Split(repoUrl, "/")
	gitProviderHost := "https://" + gitSourceUrlParts[2]
	gitlabNamespace := gitSourceUrlParts[3]
	gitlabProjectName := gitSourceUrlParts[4]
	projectPath := gitlabNamespace + "/" + gitlabProjectName

	return fmt.Sprintf("%s/%s/-/tree/%s", gitProviderHost, projectPath, sha)
}
