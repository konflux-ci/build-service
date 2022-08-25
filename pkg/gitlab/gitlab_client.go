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
	"bytes"
	"fmt"

	"github.com/xanzy/go-gitlab"
)

// Allow mocking for tests
var NewGitlabClient func(accessToken string) (*GitlabClient, error) = newGitlabClient

type GitlabClient struct {
	client *gitlab.Client
}

func newGitlabClient(accessToken string) (*GitlabClient, error) {
	glc := &GitlabClient{}
	c, err := gitlab.NewClient(accessToken)
	if err != nil {
		return nil, err
	}
	glc.client = c

	return glc, nil
}

func (c *GitlabClient) branchExist(projectPath, branchName string) (bool, error) {
	_, resp, err := c.client.Branches.GetBranch(projectPath, branchName)
	if err != nil {
		if resp.StatusCode == 404 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *GitlabClient) createBranch(projectPath, branchName, baseBranchName string) error {
	opts := &gitlab.CreateBranchOptions{
		Branch: &branchName,
		Ref:    &baseBranchName,
	}
	_, _, err := c.client.Branches.CreateBranch(projectPath, opts)
	return err
}

func (c *GitlabClient) deleteBranch(projectPath, branchName string) error {
	_, err := c.client.Branches.DeleteBranch(projectPath, branchName)
	return err
}

func (c *GitlabClient) filesUpToDate(projectPath, branchName string, files []File) (bool, error) {
	for _, file := range files {
		opts := &gitlab.GetRawFileOptions{
			Ref: &branchName,
		}
		fileContent, resp, err := c.client.RepositoryFiles.GetRawFile(projectPath, file.FullPath, opts)
		if err != nil {
			if resp.StatusCode != 404 {
				return false, err
			}
			return false, nil
		}
		if !bytes.Equal(fileContent, file.Content) {
			return false, nil
		}
	}
	return true, nil
}

func (c *GitlabClient) commitFilesIntoBranch(projectPath, branchName, commitMessage, authorName, authorEmail string, files []File) error {
	actions := []*gitlab.CommitActionOptions{}
	for _, file := range files {
		filePath := file.FullPath
		content := string(file.Content)
		var fileAction gitlab.FileActionValue

		// Detect file action: update or create
		opts := &gitlab.GetRawFileOptions{Ref: &branchName}
		_, resp, err := c.client.RepositoryFiles.GetRawFile(projectPath, file.FullPath, opts)
		if err != nil {
			if resp.StatusCode != 404 {
				return err
			}
			fileAction = gitlab.FileCreate
		} else {
			fileAction = gitlab.FileUpdate
		}

		action := &gitlab.CommitActionOptions{
			Action:   &fileAction,
			FilePath: &filePath,
			Content:  &content,
		}

		actions = append(actions, action)
	}

	opts := &gitlab.CreateCommitOptions{
		Branch:        &branchName,
		CommitMessage: &commitMessage,
		AuthorName:    &authorName,
		AuthorEmail:   &authorEmail,
		Actions:       actions,
	}
	_, _, err := c.client.Commits.CreateCommit(projectPath, opts)
	return err
}

func (c *GitlabClient) diffNotEmpty(projectPath, branchName, baseBranchName string) (bool, error) {
	straight := false
	opts := &gitlab.CompareOptions{
		From:     &baseBranchName,
		To:       &branchName,
		Straight: &straight,
	}
	cmpres, _, err := c.client.Repositories.Compare(projectPath, opts)
	if err != nil {
		return false, err
	}
	return len(cmpres.Diffs) > 0, nil
}

func (c *GitlabClient) findMergeRequestByBranches(projectPath, branch, targetBranch string) (*gitlab.MergeRequest, error) {
	openedState := "opened"
	viewType := "simple"
	opts := &gitlab.ListProjectMergeRequestsOptions{
		State:        &openedState,
		SourceBranch: &branch,
		TargetBranch: &targetBranch,
		View:         &viewType,
	}
	mrs, _, err := c.client.MergeRequests.ListProjectMergeRequests(projectPath, opts)
	if err != nil {
		return nil, err
	}
	switch len(mrs) {
	case 0:
		return nil, nil
	case 1:
		return mrs[0], nil
	default:
		return nil, fmt.Errorf("failed to find merge request by branch: %d matches found", len(mrs))
	}
}

func (c *GitlabClient) createMergeRequestWithinRepository(projectPath, branchName, baseBranchName, mrTitle, mrText string) (string, error) {
	opts := &gitlab.CreateMergeRequestOptions{
		SourceBranch: &branchName,
		TargetBranch: &baseBranchName,
		Title:        &mrTitle,
		Description:  &mrText,
	}
	mr, _, err := c.client.MergeRequests.CreateMergeRequest(projectPath, opts)
	if err != nil {
		return "", err
	}
	return mr.WebURL, nil
}

func (c *GitlabClient) getWebhookByTargetUrl(projectPath, webhookTargetUrl string) (*gitlab.ProjectHook, error) {
	opts := &gitlab.ListProjectHooksOptions{PerPage: 100}
	webhooks, _, err := c.client.Projects.ListProjectHooks(projectPath, opts)
	if err != nil {
		return nil, err
	}
	for _, webhook := range webhooks {
		if webhook.URL == webhookTargetUrl {
			return webhook, nil
		}
	}
	// Webhook with the given URL not found
	return nil, nil
}

func (c *GitlabClient) createPaCWebhook(projectPath, webhookTargetUrl, webhookSecret string) (*gitlab.ProjectHook, error) {
	opts := getPaCWebhookOpts(webhookTargetUrl, webhookSecret)
	hook, _, err := c.client.Projects.AddProjectHook(projectPath, opts)
	return hook, err
}

func (c *GitlabClient) updatePaCWebhook(projectPath string, webhookId int, webhookTargetUrl, webhookSecret string) (*gitlab.ProjectHook, error) {
	opts := gitlab.EditProjectHookOptions(*getPaCWebhookOpts(webhookTargetUrl, webhookSecret))
	hook, _, err := c.client.Projects.EditProjectHook(projectPath, webhookId, &opts)
	return hook, err
}

func getPaCWebhookOpts(webhookTargetUrl, webhookSecret string) *gitlab.AddProjectHookOptions {
	enableSSLVerification := false

	mergeRequestsEvents := true
	pushEvents := true
	noteEvents := true

	return &gitlab.AddProjectHookOptions{
		URL:                   &webhookTargetUrl,
		Token:                 &webhookSecret,
		EnableSSLVerification: &enableSSLVerification,
		MergeRequestsEvents:   &mergeRequestsEvents,
		PushEvents:            &pushEvents,
		NoteEvents:            &noteEvents,
	}
}
