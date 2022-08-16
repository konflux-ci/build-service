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
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	ghinstallation "github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v45/github"
	"golang.org/x/oauth2"
)

// Allow mocking for tests
var NewGithubClientByApp func(appId int64, privateKeyPem []byte, owner string) (*GithubClient, error) = newGithubClientByApp
var NewGithubClient func(accessToken string) *GithubClient = newGithubClient

type GithubClient struct {
	ctx    context.Context
	client *github.Client
}

func newGithubClient(accessToken string) *GithubClient {
	gh := &GithubClient{}
	gh.ctx = context.Background()

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: accessToken},
	)
	tc := oauth2.NewClient(gh.ctx, ts)

	gh.client = github.NewClient(tc)

	return gh
}

func newGithubClientByApp(appId int64, privateKeyPem []byte, owner string) (*GithubClient, error) {
	itr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appId, privateKeyPem) // 172616 (appstudio) 184730(Michkov)
	if err != nil {
		return nil, err
	}
	client := github.NewClient(&http.Client{Transport: itr})
	if err != nil {
		return nil, err
	}
	installations, _, err := client.Apps.ListInstallations(context.Background(), &github.ListOptions{})
	if err != nil {
		return nil, err
	}
	var installID int64
	for _, val := range installations {
		if val.GetAccount().GetLogin() == owner {
			installID = val.GetID()
		}
	}
	token, _, err := client.Apps.CreateInstallationToken(
		context.Background(),
		installID,
		&github.InstallationTokenOptions{})
	if err != nil {
		return nil, err
	}

	return NewGithubClient(token.GetToken()), nil
}

func (c *GithubClient) referenceExist(owner, repository, branch string) (bool, error) {
	_, resp, err := c.client.Git.GetRef(c.ctx, owner, repository, "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}

	if resp.StatusCode == 404 {
		return false, nil
	}
	return false, err
}

func (c *GithubClient) getReference(owner, repository, branch string) (*github.Reference, error) {
	ref, _, err := c.client.Git.GetRef(c.ctx, owner, repository, "refs/heads/"+branch)
	return ref, err
}

func (c *GithubClient) createReference(owner, repository, branch, baseBranch string) (*github.Reference, error) {
	baseBranchRef, err := c.getReference(owner, repository, baseBranch)
	if err != nil {
		return nil, err
	}
	newBranchRef := &github.Reference{
		Ref:    github.String("refs/heads/" + branch),
		Object: &github.GitObject{SHA: baseBranchRef.Object.SHA},
	}
	ref, _, err := c.client.Git.CreateRef(c.ctx, owner, repository, newBranchRef)
	return ref, err
}

func (c *GithubClient) deleteReference(owner, repository, branch string) error {
	_, err := c.client.Git.DeleteRef(c.ctx, owner, repository, "refs/heads/"+branch)
	return err
}

func (c *GithubClient) filesUpToDate(owner, repository, branch string, files []File) (bool, error) {
	for _, file := range files {
		opts := &github.RepositoryContentGetOptions{
			Ref: "refs/heads/" + branch,
		}

		fileContentReader, resp, err := c.client.Repositories.DownloadContents(c.ctx, owner, repository, file.Name, opts)
		if err != nil {
			// It's not clear when it returns 404 or 200 with the error message. Check both.
			if resp.StatusCode == 404 || strings.Contains(err.Error(), "no file named") {
				// Given file not found
				return false, nil
			}
			return false, err
		}
		fileContent, err := ioutil.ReadAll(fileContentReader)
		if err != nil {
			return false, err
		}

		if !bytes.Equal(fileContent, file.Content) {
			return false, nil
		}
	}
	return true, nil
}

func (c *GithubClient) createTree(owner, repository string, baseRef *github.Reference, files []File) (tree *github.Tree, err error) {
	// Load each file into the tree.
	entries := []*github.TreeEntry{}
	for _, file := range files {
		entries = append(entries, &github.TreeEntry{Path: github.String(file.Name), Type: github.String("blob"), Content: github.String(string(file.Content)), Mode: github.String("100644")})
	}

	tree, _, err = c.client.Git.CreateTree(c.ctx, owner, repository, *baseRef.Object.SHA, entries)
	return tree, err
}

func (c *GithubClient) addCommitToBranch(owner, repository, authorName, authorEmail, commitMessage string, files []File, ref *github.Reference) error {
	// Get the parent commit to attach the commit to.
	parent, _, err := c.client.Repositories.GetCommit(c.ctx, owner, repository, *ref.Object.SHA, nil)
	if err != nil {
		return err
	}
	// This is not always populated, but is needed.
	parent.Commit.SHA = parent.SHA

	tree, err := c.createTree(owner, repository, ref, files)
	if err != nil {
		return err
	}

	// Create the commit using the tree.
	date := time.Now()
	author := &github.CommitAuthor{Date: &date, Name: &authorName, Email: &authorEmail}
	commit := &github.Commit{Author: author, Message: &commitMessage, Tree: tree, Parents: []*github.Commit{parent.Commit}}
	newCommit, _, err := c.client.Git.CreateCommit(c.ctx, owner, repository, commit)
	if err != nil {
		return err
	}

	// Attach the created commit to the given branch.
	ref.Object.SHA = newCommit.SHA
	_, _, err = c.client.Git.UpdateRef(c.ctx, owner, repository, ref, false)
	return err
}

// findPullRequestByBranchesWithinRepository searches for a PR within repository by current and target (base) branch.
func (c *GithubClient) findPullRequestByBranchesWithinRepository(owner, repository, branchName, baseBranchName string) (*github.PullRequest, error) {
	opts := &github.PullRequestListOptions{
		State:       "open",
		Base:        baseBranchName,
		Head:        owner + ":" + branchName,
		ListOptions: github.ListOptions{PerPage: 100},
	}
	prs, _, err := c.client.PullRequests.List(c.ctx, owner, repository, opts)
	if err != nil {
		return nil, err
	}
	switch len(prs) {
	case 0:
		return nil, nil
	case 1:
		return prs[0], nil
	default:
		return nil, fmt.Errorf("failed to find pull request by branch %s: %d matches found", opts.Head, len(prs))
	}
}

// createPullRequestWithinRepository create a new pull request into the same repository.
// Returns url to the created pull request.
func (c *GithubClient) createPullRequestWithinRepository(owner, repository, branchName, baseBranchName, prTitle, prText string) (string, error) {
	branch := fmt.Sprintf("%s:%s", owner, branchName)

	newPRData := &github.NewPullRequest{
		Title:               &prTitle,
		Head:                &branch,
		Base:                &baseBranchName,
		Body:                &prText,
		MaintainerCanModify: github.Bool(true),
	}

	pr, _, err := c.client.PullRequests.Create(c.ctx, owner, repository, newPRData)
	if err != nil {
		return "", err
	}

	return pr.GetHTMLURL(), nil
}

// getWebhookByTargetUrl returns webhook by its target url or nil if such webhook doesn't exist.
func (c *GithubClient) getWebhookByTargetUrl(owner, repository, webhookTargetUrl string) (*github.Hook, error) {
	// Suppose that the repository does not have more than 100 webhooks
	listOpts := &github.ListOptions{PerPage: 100}
	webhooks, _, err := c.client.Repositories.ListHooks(c.ctx, owner, repository, listOpts)
	if err != nil {
		return nil, err
	}

	for _, webhook := range webhooks {
		if webhook.Config["url"] == webhookTargetUrl {
			return webhook, nil
		}
	}
	// Webhook with the given URL not found
	return nil, nil
}

func (c *GithubClient) createWebhook(owner, repository string, webhook *github.Hook) (*github.Hook, error) {
	webhook, _, err := c.client.Repositories.CreateHook(c.ctx, owner, repository, webhook)
	return webhook, err
}

func (c *GithubClient) updateWebhook(owner, repository string, webhook *github.Hook) (*github.Hook, error) {
	webhook, _, err := c.client.Repositories.EditHook(c.ctx, owner, repository, *webhook.ID, webhook)
	return webhook, err
}
