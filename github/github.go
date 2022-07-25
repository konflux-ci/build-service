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
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	ghinstallation "github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v45/github"
	"golang.org/x/oauth2"
)

type File struct {
	Name    string
	Content []byte
}

type CommitPR struct {
	ctx           context.Context
	client        *github.Client
	SourceOwner   string
	SourceRepo    string
	PRRepoOwner   string
	PRRepo        string
	CommitMessage string
	CommitBranch  string
	BaseBranch    string
	PRTitle       string
	PRText        string
	Files         []File
	AuthorName    string
	AuthorEmail   string
}

// Allow mocking for tests
var CreateCommitAndPR func(c *CommitPR, appId int64, privatePem []byte) error = (*CommitPR).createCommitAndPR

// getRef returns the commit branch reference object if it exists or creates it
// from the base branch before returning it.
func (c *CommitPR) getRef() (ref *github.Reference, err error) {
	if ref, _, err = c.client.Git.GetRef(c.ctx, c.SourceOwner, c.SourceRepo, "refs/heads/"+c.CommitBranch); err == nil {
		return ref, nil
	}

	// We consider that an error means the branch has not been found and needs to
	// be created.
	if c.CommitBranch == c.BaseBranch {
		return nil, errors.New("the commit branch does not exist but `-base-branch` is the same as `-commit-branch`")
	}

	if c.BaseBranch == "" {
		return nil, errors.New("the `-base-branch` should not be set to an empty string when the branch specified by `-commit-branch` does not exists")
	}

	var baseRef *github.Reference
	if baseRef, _, err = c.client.Git.GetRef(c.ctx, c.SourceOwner, c.SourceRepo, "refs/heads/"+c.BaseBranch); err != nil {
		return nil, err
	}
	newRef := &github.Reference{Ref: github.String("refs/heads/" + c.CommitBranch), Object: &github.GitObject{SHA: baseRef.Object.SHA}}
	ref, _, err = c.client.Git.CreateRef(c.ctx, c.SourceOwner, c.SourceRepo, newRef)
	return ref, err
}

// getTree generates the tree to commit based on the given files and the commit
// of the ref you got in getRef.
func (c *CommitPR) getTree(ref *github.Reference) (tree *github.Tree, err error) {
	// Create a tree with what to commit.
	entries := []*github.TreeEntry{}

	// Load each file into the tree.
	for _, file := range c.Files {
		// file, content, err := getFileContent(fileArg)
		// if err != nil {
		// 	return nil, err
		// }
		entries = append(entries, &github.TreeEntry{Path: github.String(file.Name), Type: github.String("blob"), Content: github.String(string(file.Content)), Mode: github.String("100644")})
	}

	tree, _, err = c.client.Git.CreateTree(c.ctx, c.SourceOwner, c.SourceRepo, *ref.Object.SHA, entries)
	return tree, err
}

// pushCommit creates the commit in the given reference using the given tree.
func (c *CommitPR) pushCommit(ref *github.Reference, tree *github.Tree) (err error) {
	// Get the parent commit to attach the commit to.
	parent, _, err := c.client.Repositories.GetCommit(c.ctx, c.SourceOwner, c.SourceRepo, *ref.Object.SHA, nil)
	if err != nil {
		return err
	}
	// This is not always populated, but is needed.
	parent.Commit.SHA = parent.SHA

	// Create the commit using the tree.
	date := time.Now()
	author := &github.CommitAuthor{Date: &date, Name: &c.AuthorName, Email: &c.AuthorEmail}
	commit := &github.Commit{Author: author, Message: &c.CommitMessage, Tree: tree, Parents: []*github.Commit{parent.Commit}}
	newCommit, _, err := c.client.Git.CreateCommit(c.ctx, c.SourceOwner, c.SourceRepo, commit)
	if err != nil {
		return err
	}

	// Attach the commit to the master branch.
	ref.Object.SHA = newCommit.SHA
	_, _, err = c.client.Git.UpdateRef(c.ctx, c.SourceOwner, c.SourceRepo, ref, false)
	return err
}

// createPR creates a pull request. Based on: https://godoc.org/github.com/google/go-github/github#example-PullRequestsService-Create
func (c *CommitPR) createPR() (err error) {
	if c.PRTitle == "" {
		return errors.New("missing `-pr-title` flag; skipping PR creation")
	}

	if c.PRRepoOwner != "" && c.PRRepoOwner != c.SourceOwner {
		c.CommitBranch = fmt.Sprintf("%s:%s", c.SourceOwner, c.CommitBranch)
	} else {
		c.PRRepoOwner = c.SourceOwner
	}

	if c.PRRepo == "" {
		c.PRRepo = c.SourceRepo
	}

	newPR := &github.NewPullRequest{
		Title:               &c.PRTitle,
		Head:                &c.CommitBranch,
		Base:                &c.BaseBranch,
		Body:                &c.PRText,
		MaintainerCanModify: github.Bool(true),
	}

	pr, _, err := c.client.PullRequests.Create(c.ctx, c.PRRepoOwner, c.PRRepo, newPR)
	if err != nil {
		// Unfortunately, it's not possible to detect this error via an error code.
		if strings.Contains(err.Error(), "pull request already exists") {
			fmt.Printf("PaC integration PR already exists\n")
			return nil
		}
		return err
	}

	fmt.Printf("PR created: %s\n", pr.GetHTMLURL())
	return nil
}

func (c *CommitPR) getAuthApp(appId int64, privatePem []byte) (err error) {
	itr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appId, privatePem) // 172616 (appstudio) 184730(Michkov)
	if err != nil {
		return err
	}
	client := github.NewClient(&http.Client{Transport: itr})
	if err != nil {
		return err
	}
	installations, _, err := client.Apps.ListInstallations(context.Background(), &github.ListOptions{})
	if err != nil {
		return err
	}
	var installID int64
	for _, val := range installations {
		if val.GetAccount().GetLogin() == c.SourceOwner {
			installID = val.GetID()
		}
	}
	token, _, err := client.Apps.CreateInstallationToken(
		context.Background(),
		installID,
		&github.InstallationTokenOptions{})
	if err != nil {
		return err
	}
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token.GetToken()},
	)
	oAuthClient := oauth2.NewClient(context.Background(), ts)

	c.client = github.NewClient(oAuthClient)
	if err != nil {
		return err
	}
	return nil
}

func (c *CommitPR) createCommitAndPR(appId int64, privatePem []byte) (err error) {
	c.ctx = context.Background()
	err = c.getAuthApp(appId, privatePem)
	if err != nil {
		return err
	}
	ref, err := c.getRef()
	if err != nil {
		return err
	}
	tree, err := c.getTree(ref)
	if err != nil {
		return err
	}

	if err := c.pushCommit(ref, tree); err != nil {
		return err
	}

	if err := c.createPR(); err != nil {
		return err
	}
	return nil
}
