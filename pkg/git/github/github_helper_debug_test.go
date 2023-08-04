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
	"os"
	"strings"
	"testing"

	gp "github.com/redhat-appstudio/build-service/pkg/git/gitprovider"
)

// THIS FILE IS NOT UNIT TESTS
// Put your own data below and select a way how to create GitHub client to debug interactions with GitHub
var (
	repoUrl = "https://github.com/user/test-component-repository"
	// Webhook
	accessToken = "ghp_token"
	// Application
	githubAppId             int64 = 217385
	githubAppPrivateKeyPath       = "/home/user/appstudio-build-pac-private-key.pem"

	// A way how to create GitHub client
	ghClientType = githubNone
)

type githubClientCreationWay string

const (
	githubApp             githubClientCreationWay = "github-app"
	githubAppForeignToken githubClientCreationWay = "github-app-foreign"
	githubToken           githubClientCreationWay = "token"
	githubNone            githubClientCreationWay = "none"
)

func createClient(clientType githubClientCreationWay) *GithubClient {
	switch clientType {
	case githubApp:
		githubAppPrivateKey, err := os.ReadFile(githubAppPrivateKeyPath)
		if err != nil {
			fmt.Printf("Cannot read private key file by path: %s", githubAppPrivateKeyPath)
			return nil
		}
		owner, _ := getOwnerAndRepoFromUrl(repoUrl)
		ghclient, err := NewGithubClientByApp(githubAppId, []byte(githubAppPrivateKey), owner)
		if err != nil {
			fmt.Printf("error: %v", err)
		}
		return ghclient

	case githubAppForeignToken:
		githubAppPrivateKey, err := os.ReadFile(githubAppPrivateKeyPath)
		if err != nil {
			fmt.Printf("Cannot read private key file by path: %s", githubAppPrivateKeyPath)
			return nil
		}
		ghclient, err := newGithubClientForSimpleBuildByApp(githubAppId, []byte(githubAppPrivateKey))
		if err != nil {
			fmt.Printf("error: %v", err)
		}
		return ghclient

	case githubToken:
		return NewGithubClient(accessToken)
	default:
		return nil
	}
}

func TestCreatePaCPullRequest(t *testing.T) {
	ghclient := createClient(ghClientType)
	if ghclient == nil {
		return
	}

	pipelineOnPush := []byte("pipelineOnPush:\n  bundle: 'test-bundle-1'\n  when: 'on-push'\n")
	pipelineOnPR := []byte("pipelineOnPR:\n  bundle: 'test-bundle-2'\n  when: 'on-pr'\n")

	componentName := "unittest-component-name"
	prData := &gp.MergeRequestData{
		CommitMessage:  "Appstudio update " + componentName,
		BranchName:     "appstudio-" + componentName,
		BaseBranchName: "",
		Title:          "Appstudio update " + componentName,
		Text:           "Pipelines as Code configuration proposal",
		AuthorName:     "redhat-appstudio",
		AuthorEmail:    "rhtap@redhat.com",
		Files: []gp.RepositoryFile{
			{FullPath: ".tekton/" + componentName + "-push.yaml", Content: pipelineOnPush},
			{FullPath: ".tekton/" + componentName + "-pull-request.yaml", Content: pipelineOnPR},
		},
	}

	url, err := ghclient.EnsurePaCMergeRequest(repoUrl, prData)
	if err != nil {
		t.Fatal(err)
	}
	if url != "" && !strings.HasPrefix(url, "http") {
		t.Fatal("Pull Request URL must not be empty")
	}
}

func TestUndoPaCPullRequest(t *testing.T) {
	ghclient := createClient(ghClientType)
	if ghclient == nil {
		return
	}

	componentName := "unittest-component-name"
	prData := &gp.MergeRequestData{
		CommitMessage:  "Appstudio purge " + componentName,
		BranchName:     "appstudio-purge-" + componentName,
		BaseBranchName: "",
		Title:          "Appstudio purge " + componentName,
		Text:           "Pipelines as Code configuration removal",
		AuthorName:     "redhat-appstudio",
		AuthorEmail:    "rhtap@redhat.com",
		Files: []gp.RepositoryFile{
			{FullPath: ".tekton/" + componentName + "-push.yaml"},
			{FullPath: ".tekton/" + componentName + "-pull-request.yaml"},
		},
	}

	url, err := ghclient.UndoPaCMergeRequest(repoUrl, prData)
	if err != nil {
		t.Fatal(err)
	}
	if url != "" && !strings.HasPrefix(url, "http") {
		t.Fatal("Pull Request URL must not be empty")
	}
}

func TestSetupPaCWebhook(t *testing.T) {
	ghclient := createClient(ghClientType)
	if ghclient == nil {
		return
	}

	targetWebhookUrl := "https://pac.route.my-cluster.net"
	webhookSecretString := "23f29e8f7fa8c58c1e8e50ecfbd49aec314f4908"

	err := ghclient.SetupPaCWebhook(repoUrl, targetWebhookUrl, webhookSecretString)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeletePaCWebhook(t *testing.T) {
	ghclient := createClient(ghClientType)
	if ghclient == nil {
		return
	}

	targetWebhookUrl := "https://pac.route.my-cluster.net"

	err := ghclient.DeletePaCWebhook(repoUrl, targetWebhookUrl)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetBranchSHA(t *testing.T) {
	ghclient := createClient(ghClientType)
	if ghclient == nil {
		return
	}

	branchName := "main"

	sha, err := ghclient.GetBranchSha(repoUrl, branchName)
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("Commit SHA must not be empty")
	}
}

func TestIsRepositoryPublic(t *testing.T) {
	ghclient := createClient(ghClientType)
	if ghclient == nil {
		return
	}

	isPublic, err := ghclient.IsRepositoryPublic(repoUrl)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(isPublic)
}

// Only GitHub Application client is applicable for this test
func TestIsAppInstalledIntoRepository(t *testing.T) {
	ghclient := createClient(ghClientType)
	if ghclient == nil {
		return
	}

	installed, err := ghclient.IsAppInstalledIntoRepository(repoUrl)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("The application is installed: %t into %s", installed, repoUrl)
}

// Only GitHub Application client is applicable for this test
func TestGetGitHubAppName(t *testing.T) {
	ghclient := createClient(ghClientType)
	if ghclient == nil {
		return
	}

	appName, appSlug, err := ghclient.GetConfiguredGitAppName()
	if err != nil {
		t.Fatal(err)
	}
	if appName == "" {
		t.Fatal("GitHub Application name should not be empty")
	}
	if appSlug == "" {
		t.Fatal("GitHub Application slug (URL-friendly name of GitHub App) should not be empty")
	}
}
