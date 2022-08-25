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
	"io/ioutil"
	"strings"
	"testing"
)

// THIS FILE IS NOT UNIT TESTS
// Put your own data below and comment out function override in the test to debug interactions with GitHub
var (
	repoUrl = "https://github.com/user/test-component-repository"
	// Webhook
	accessToken = "ghp_token"
	// Application
	githubAppId             int64 = 217385
	githubAppPrivateKeyPath       = "/home/user/appstudio-build-pac-private-key.pem"
)

var (
	StubCreatePaCPullRequest = func(g *GithubClient, d *PaCPullRequestData) (string, error) { return "", nil }
	StubSetupPaCWebhook      = func(g *GithubClient, webhookUrl, webhookSecret, owner, repository string) error { return nil }
)

func TestCreatePaCPullRequest(t *testing.T) {
	CreatePaCPullRequest = StubCreatePaCPullRequest

	ghclient := NewGithubClient(accessToken)

	pipelineOnPush := []byte("pipelineOnPush:\n  bundle: 'test-bundle-1'\n  when: 'on-push'\n")
	pipelineOnPR := []byte("pipelineOnPR:\n  bundle: 'test-bundle-2'\n  when: 'on-pr'\n")

	componentName := "unittest-component-name"
	gitSourceUrlParts := strings.Split(repoUrl, "/")
	prData := &PaCPullRequestData{
		Owner:         gitSourceUrlParts[3],
		Repository:    gitSourceUrlParts[4],
		CommitMessage: "Appstudio update " + componentName,
		Branch:        "appstudio-" + componentName,
		BaseBranch:    "main",
		PRTitle:       "Appstudio update " + componentName,
		PRText:        "Pipelines as Code configuration proposal",
		AuthorName:    "redhat-appstudio",
		AuthorEmail:   "appstudio@redhat.com",
		Files: []File{
			{FullPath: ".tekton/" + componentName + "-push.yaml", Content: pipelineOnPush},
			{FullPath: ".tekton/" + componentName + "-pull.yaml", Content: pipelineOnPR},
		},
	}

	url, err := CreatePaCPullRequest(ghclient, prData)
	if err != nil {
		t.Fatal(err)
	}
	if url != "" && !strings.HasPrefix(url, "http") {
		t.Fatal("Pull Request URL must not be empty")
	}
}

func TestCreatePaCPullRequestViaGitHubApplication(t *testing.T) {
	CreatePaCPullRequest = StubCreatePaCPullRequest

	pipelineOnPush := []byte("pipelineOnPush:\n  bundle: 'test-bundle-1'\n  when: 'in-push'\n")
	pipelineOnPR := []byte("pipelineOnPR:\n  bundle: 'test-bundle-2'\n  when: 'on-pr'\n")

	componentName := "unittest-component-name"
	gitSourceUrlParts := strings.Split(repoUrl, "/")
	owner := gitSourceUrlParts[3]

	githubAppPrivateKey, err := ioutil.ReadFile(githubAppPrivateKeyPath)
	if err != nil {
		// Private key file by given path doesn't exist
		return
	}

	ghclient, err := NewGithubClientByApp(githubAppId, []byte(githubAppPrivateKey), owner)
	if err != nil {
		t.Fatal(err)
	}

	prData := &PaCPullRequestData{
		Owner:         owner,
		Repository:    gitSourceUrlParts[4],
		CommitMessage: "Appstudio update " + componentName,
		Branch:        "appstudio-" + componentName,
		BaseBranch:    "main",
		PRTitle:       "Appstudio update " + componentName,
		PRText:        "Pipelines as Code configuration proposal",
		AuthorName:    "redhat-appstudio",
		AuthorEmail:   "appstudio@redhat.com",
		Files: []File{
			{FullPath: ".tekton/" + componentName + "-push.yaml", Content: pipelineOnPush},
			{FullPath: ".tekton/" + componentName + "-pull.yaml", Content: pipelineOnPR},
		},
	}

	url, err := CreatePaCPullRequest(ghclient, prData)
	if err != nil {
		t.Fatal(err)
	}
	if url != "" && !strings.HasPrefix(url, "http") {
		t.Fatal("Pull Request URL must not be empty")
	}
}

func TestSetupPaCWebhook(t *testing.T) {
	SetupPaCWebhook = StubSetupPaCWebhook

	targetWebhookUrl := "https://pac.route.my-cluster.net"
	webhookSecretString := "23f29e8f7fa8c58c1e8e50ecfbd49aec314f4908"

	ghclient := NewGithubClient(accessToken)

	gitSourceUrlParts := strings.Split(repoUrl, "/")
	owner := gitSourceUrlParts[3]
	repository := gitSourceUrlParts[4]

	err := SetupPaCWebhook(ghclient, targetWebhookUrl, webhookSecretString, owner, repository)
	if err != nil {
		t.Fatal(err)
	}
}
