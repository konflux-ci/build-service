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
	"strings"
	"testing"

	gp "github.com/redhat-appstudio/build-service/pkg/git/gitprovider"
)

// THIS FILE IS NOT UNIT TESTS
// Put your own data below and set skip tests to false to debug interactions with GitLab
var (
	repoUrl     = "https://gitlab.com/user/devfile-sample-go-basic"
	accessToken = "glpat-token"

	shouldSkipAllTests = true
)

func TestEnsurePaCMergeRequest(t *testing.T) {
	if shouldSkipAllTests {
		return
	}

	glclient, err := NewGitlabClient(accessToken)
	if err != nil {
		t.Fatal(err)
	}

	pipelineOnPush := []byte("pipelineOnPush:\n  bundle: 'test-bundle-1'\n  when: 'on-push'\n")
	pipelineOnPR := []byte("pipelineOnMR:\n  bundle: 'test-bundle-2'\n  when: 'on-mr'\n")

	componentName := "unittest-component-name"
	mrData := &gp.MergeRequestData{
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

	url, err := glclient.EnsurePaCMergeRequest(repoUrl, mrData)
	if err != nil {
		t.Fatal(err)
	}
	if url != "" && !strings.HasPrefix(url, "http") {
		t.Fatal("Merge Request URL must not be empty")
	}
}

func TestUndoPaCMergeRequest(t *testing.T) {
	if shouldSkipAllTests {
		return
	}

	glclient, err := NewGitlabClient(accessToken)
	if err != nil {
		t.Fatal(err)
	}

	componentName := "unittest-component-name"
	mrData := &gp.MergeRequestData{
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

	url, err := glclient.UndoPaCMergeRequest(repoUrl, mrData)
	if err != nil {
		t.Fatal(err)
	}
	if url != "" && !strings.HasPrefix(url, "http") {
		t.Fatal("Merge Request URL must not be empty")
	}
}

func TestSetupPaCWebhook(t *testing.T) {
	if shouldSkipAllTests {
		return
	}

	targetWebhookUrl := "https://pac.route.my-cluster.net"
	webhookSecretString := "d01b38971dad59514298d763f288392c08221043"

	glclient, err := NewGitlabClient(accessToken)
	if err != nil {
		t.Fatal(err)
	}

	err = glclient.SetupPaCWebhook(repoUrl, targetWebhookUrl, webhookSecretString)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeletePaCWebhook(t *testing.T) {
	if shouldSkipAllTests {
		return
	}

	targetWebhookUrl := "https://pac.route.my-cluster.net"

	glclient, err := NewGitlabClient(accessToken)
	if err != nil {
		t.Fatal(err)
	}

	err = glclient.DeletePaCWebhook(repoUrl, targetWebhookUrl)
	if err != nil {
		t.Fatal(err)
	}
}

func TestIsRepositoryPublic(t *testing.T) {
	if shouldSkipAllTests {
		return
	}

	glclient, err := NewGitlabClient(accessToken)
	if err != nil {
		t.Fatal(err)
	}

	isPublic, err := glclient.IsRepositoryPublic(repoUrl)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(isPublic)
}
