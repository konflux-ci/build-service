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
	"io"
	"net/http"
	"strings"
	"time"

	ghinstallation "github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v45/github"
	"github.com/redhat-appstudio/build-service/pkg/boerrors"
	"golang.org/x/oauth2"
)

// Allow mocking for tests
var NewGithubClientByApp func(appId int64, privateKeyPem []byte, owner string) (*GithubClient, error) = newGithubClientByApp
var NewGithubClient func(accessToken string) *GithubClient = newGithubClient
var GetInstallations func(appId int64, privateKeyPem []byte) ([]ApplicationInstallation, string, error) = getInstallations

type GithubClient struct {
	ctx    context.Context
	client *github.Client
}

type ApplicationInstallation struct {
	Token          string
	InstallationID int64
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
		// Inability to create transport based on a private key indicates that the key is bad formatted
		return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppMalformedPrivateKey, err)
	}
	client := github.NewClient(&http.Client{Transport: itr})

	var installId int64
	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for installId == 0 {
		installations, resp, err := client.Apps.ListInstallations(context.Background(), &opt.ListOptions)
		if err != nil {
			if resp != nil && resp.Response != nil && resp.Response.StatusCode != 0 {
				switch resp.StatusCode {
				case 401:
					return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppPrivateKeyNotMatched, err)
				case 404:
					return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppDoesNotExist, err)
				}
			}
			return nil, boerrors.NewBuildOpError(boerrors.ETransientError, err)
		}
		for _, val := range installations {
			if val.GetAccount().GetLogin() == owner {
				installId = val.GetID()
				break
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	if installId == 0 {
		err := fmt.Errorf("unable to find GitHub InstallationID for user %s", owner)
		return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppNotInstalled, err)
	}
	// The user has the application installed,
	// but it doesn't guarantee that the application is installed into all user's repositories.

	token, _, err := client.Apps.CreateInstallationToken(
		context.Background(),
		installId,
		&github.InstallationTokenOptions{})
	if err != nil {
		// TODO analyze the error
		return nil, err
	}

	return NewGithubClient(token.GetToken()), nil
}

func getInstallations(appId int64, privateKeyPem []byte) ([]ApplicationInstallation, string, error) {
	itr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appId, privateKeyPem)
	if err != nil {
		// Inability to create transport based on a private key indicates that the key is bad formatted
		return nil, "", boerrors.NewBuildOpError(boerrors.EGitHubAppMalformedPrivateKey, err)
	}
	client := github.NewClient(&http.Client{Transport: itr})
	appInstallations := []ApplicationInstallation{}
	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	githubApp, _, err := client.Apps.Get(context.Background(), "")
	if err != nil {
		return nil, "", fmt.Errorf("failed to load GitHub app metadata, %w", err)
	}
	slug := (githubApp.GetSlug())
	for {
		installations, resp, err := client.Apps.ListInstallations(context.Background(), &opt.ListOptions)
		if err != nil {
			if resp != nil && resp.Response != nil && resp.Response.StatusCode != 0 {
				switch resp.StatusCode {
				case 401:
					return nil, "", boerrors.NewBuildOpError(boerrors.EGitHubAppPrivateKeyNotMatched, err)
				case 404:
					return nil, "", boerrors.NewBuildOpError(boerrors.EGitHubAppDoesNotExist, err)
				}
			}
			return nil, "", boerrors.NewBuildOpError(boerrors.ETransientError, err)
		}
		for _, val := range installations {
			token, _, err := client.Apps.CreateInstallationToken(
				context.Background(),
				*val.ID,
				&github.InstallationTokenOptions{})
			if err != nil {
				// TODO analyze the error
				continue
			}
			appInstallations = append(appInstallations, ApplicationInstallation{
				Token:          token.GetToken(),
				InstallationID: *val.ID,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return appInstallations, slug, nil
}

// isAppInstalledIntoRepository finds out if the application is installed into given repository.
// The application is identified by it's installation token, i.e. the client itself must be created
// from an application installation token. See newGithubClientByApp for details.
// This method should be used only with clients created by newGithubClientByApp.
func (c *GithubClient) isAppInstalledIntoRepository(owner, repository string) (bool, error) {
	listOpts := &github.ListOptions{PerPage: 100}
	for {
		repositoriesListPage, resp, err := c.client.Apps.ListRepos(c.ctx, listOpts)
		if err != nil {
			return false, err
		}
		for _, repo := range repositoriesListPage.Repositories {
			if *repo.Name == repository && *repo.Owner.Login == owner {
				return true, nil
			}
		}
		if resp.NextPage == 0 {
			break
		}
		listOpts.Page = resp.NextPage
	}
	return false, nil
}

func (c *GithubClient) referenceExist(owner, repository, branch string) (bool, error) {
	_, resp, err := c.client.Git.GetRef(c.ctx, owner, repository, "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	switch resp.StatusCode {
	case 401:
		return false, boerrors.NewBuildOpError(boerrors.EGitHubTokenUnauthorized, err)
	case 404:
		return false, nil
	}

	return false, err
}

func (c *GithubClient) getReference(owner, repository, branch string) (*github.Reference, error) {
	ref, resp, err := c.client.Git.GetRef(c.ctx, owner, repository, "refs/heads/"+branch)
	return ref, RefineGitHostingServiceError(resp.Response, err)
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
	ref, resp, err := c.client.Git.CreateRef(c.ctx, owner, repository, newBranchRef)
	return ref, RefineGitHostingServiceError(resp.Response, err)
}

func (c *GithubClient) deleteReference(owner, repository, branch string) error {
	resp, err := c.client.Git.DeleteRef(c.ctx, owner, repository, "refs/heads/"+branch)
	return RefineGitHostingServiceError(resp.Response, err)
}

func (c *GithubClient) getDefaultBranch(owner, repository string) (string, error) {
	repositoryInfo, resp, err := c.client.Repositories.Get(c.ctx, owner, repository)
	if err != nil {
		return "", RefineGitHostingServiceError(resp.Response, err)
	}
	if repositoryInfo == nil {
		return "", fmt.Errorf("repository info is empty in GitHub API response")
	}
	return *repositoryInfo.DefaultBranch, nil
}

func (c *GithubClient) filesUpToDate(owner, repository, branch string, files []File) (bool, error) {
	for _, file := range files {
		opts := &github.RepositoryContentGetOptions{
			Ref: "refs/heads/" + branch,
		}

		fileContentReader, resp, err := c.client.Repositories.DownloadContents(c.ctx, owner, repository, file.FullPath, opts)
		if err != nil {
			// It's not clear when it returns 404 or 200 with the error message. Check both.
			if resp.StatusCode == 404 || strings.Contains(err.Error(), "no file named") {
				// Given file not found
				return false, nil
			}

			return false, RefineGitHostingServiceError(resp.Response, err)
		}
		fileContent, err := io.ReadAll(fileContentReader)
		if err != nil {
			return false, err
		}

		if !bytes.Equal(fileContent, file.Content) {
			return false, nil
		}
	}
	return true, nil
}

// filesExistInDirectory checks if given files exist under specified directory.
// Returns subset of given files which exist.
func (c *GithubClient) filesExistInDirectory(owner, repository, branch, directoryPath string, files []File) ([]File, error) {
	existingFiles := make([]File, 0, len(files))

	opts := &github.RepositoryContentGetOptions{
		Ref: "refs/heads/" + branch,
	}
	_, dirContent, resp, err := c.client.Repositories.GetContents(c.ctx, owner, repository, directoryPath, opts)
	if err != nil {
		switch resp.StatusCode {
		case 401:
			return existingFiles, boerrors.NewBuildOpError(boerrors.EGitHubTokenUnauthorized, err)
		case 404:
			return existingFiles, nil
		}
		return existingFiles, err
	}

	for _, file := range dirContent {
		if file.GetType() != "file" {
			continue
		}
		for _, f := range files {
			if file.GetPath() == f.FullPath {
				existingFiles = append(existingFiles, File{FullPath: file.GetPath()})
				break
			}
		}
	}

	return existingFiles, nil
}

func (c *GithubClient) createTree(owner, repository string, baseRef *github.Reference, files []File) (tree *github.Tree, err error) {
	// Load each file into the tree.
	entries := []*github.TreeEntry{}
	for _, file := range files {
		entries = append(entries, &github.TreeEntry{Path: github.String(file.FullPath), Type: github.String("blob"), Content: github.String(string(file.Content)), Mode: github.String("100644")})
	}

	tree, resp, err := c.client.Git.CreateTree(c.ctx, owner, repository, *baseRef.Object.SHA, entries)
	return tree, RefineGitHostingServiceError(resp.Response, err)
}

func (c *GithubClient) deleteFromTree(owner, repository string, baseRef *github.Reference, files []File) (tree *github.Tree, err error) {
	// Delete each file from the tree.
	entries := []*github.TreeEntry{}
	for _, file := range files {
		entries = append(entries, &github.TreeEntry{
			Path: github.String(file.FullPath),
			Type: github.String("blob"),
			Mode: github.String("100644"),
		})
	}

	tree, resp, err := c.client.Git.CreateTree(c.ctx, owner, repository, *baseRef.Object.SHA, entries)
	return tree, RefineGitHostingServiceError(resp.Response, err)
}

func (c *GithubClient) addCommitToBranch(owner, repository, authorName, authorEmail, commitMessage string, files []File, ref *github.Reference) error {
	// Get the parent commit to attach the commit to.
	parent, resp, err := c.client.Repositories.GetCommit(c.ctx, owner, repository, *ref.Object.SHA, nil)
	if err != nil {
		return RefineGitHostingServiceError(resp.Response, err)
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
	newCommit, resp, err := c.client.Git.CreateCommit(c.ctx, owner, repository, commit)
	if err != nil {
		return RefineGitHostingServiceError(resp.Response, err)
	}

	// Attach the created commit to the given branch.
	ref.Object.SHA = newCommit.SHA
	_, resp, err = c.client.Git.UpdateRef(c.ctx, owner, repository, ref, false)
	return RefineGitHostingServiceError(resp.Response, err)
}

// Creates commit into specified branch that deletes given files.
func (c *GithubClient) addDeleteCommitToBranch(owner, repository, authorName, authorEmail, commitMessage string, files []File, ref *github.Reference) error {
	// Get the parent commit to attach the commit to.
	parent, resp, err := c.client.Repositories.GetCommit(c.ctx, owner, repository, *ref.Object.SHA, nil)
	if err != nil {
		return RefineGitHostingServiceError(resp.Response, err)
	}
	// This is not always populated, but needed.
	parent.Commit.SHA = parent.SHA

	tree, err := c.deleteFromTree(owner, repository, ref, files)
	if err != nil {
		return err
	}

	// Create the commit using the tree.
	date := time.Now()
	author := &github.CommitAuthor{Date: &date, Name: &authorName, Email: &authorEmail}
	commit := &github.Commit{Author: author, Message: &commitMessage, Tree: tree, Parents: []*github.Commit{parent.Commit}}
	newCommit, resp, err := c.client.Git.CreateCommit(c.ctx, owner, repository, commit)
	if err != nil {
		return RefineGitHostingServiceError(resp.Response, err)
	}

	// Attach the created commit to the given branch.
	ref.Object.SHA = newCommit.SHA
	_, resp, err = c.client.Git.UpdateRef(c.ctx, owner, repository, ref, false)
	return RefineGitHostingServiceError(resp.Response, err)
}

// findPullRequestByBranchesWithinRepository searches for a PR within repository by current and target (base) branch.
func (c *GithubClient) findPullRequestByBranchesWithinRepository(owner, repository, branchName, baseBranchName string) (*github.PullRequest, error) {
	opts := &github.PullRequestListOptions{
		State:       "open",
		Base:        baseBranchName,
		Head:        owner + ":" + branchName,
		ListOptions: github.ListOptions{PerPage: 100},
	}
	prs, resp, err := c.client.PullRequests.List(c.ctx, owner, repository, opts)
	if err != nil {
		return nil, RefineGitHostingServiceError(resp.Response, err)
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

	pr, resp, err := c.client.PullRequests.Create(c.ctx, owner, repository, newPRData)
	if err != nil {
		return "", RefineGitHostingServiceError(resp.Response, err)
	}

	return pr.GetHTMLURL(), nil
}

// getWebhookByTargetUrl returns webhook by its target url or nil if such webhook doesn't exist.
func (c *GithubClient) getWebhookByTargetUrl(owner, repository, webhookTargetUrl string) (*github.Hook, error) {
	// Suppose that the repository does not have more than 100 webhooks
	listOpts := &github.ListOptions{PerPage: 100}
	webhooks, resp, err := c.client.Repositories.ListHooks(c.ctx, owner, repository, listOpts)
	if err != nil {
		return nil, RefineGitHostingServiceError(resp.Response, err)
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
	webhook, resp, err := c.client.Repositories.CreateHook(c.ctx, owner, repository, webhook)
	return webhook, RefineGitHostingServiceError(resp.Response, err)
}

func (c *GithubClient) updateWebhook(owner, repository string, webhook *github.Hook) (*github.Hook, error) {
	webhook, resp, err := c.client.Repositories.EditHook(c.ctx, owner, repository, *webhook.ID, webhook)
	return webhook, RefineGitHostingServiceError(resp.Response, err)
}

func (c *GithubClient) deleteWebhook(owner, repository string, webhookId int64) error {
	resp, err := c.client.Repositories.DeleteHook(c.ctx, owner, repository, webhookId)
	if err != nil {
		switch resp.StatusCode {
		case 401:
			return boerrors.NewBuildOpError(boerrors.EGitHubTokenUnauthorized, err)
		case 404:
			// Note: GitHub responds 404 in the following two cases:
			// 1) delete a nonexisting hook with sufficient scope
			// 2) delete an existing hook without sufficient scope.
			return nil
		}
	}
	return nil
}
