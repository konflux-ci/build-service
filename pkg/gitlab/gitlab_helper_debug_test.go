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
	"strings"
	"testing"
)

// THIS FILE IS NOT UNIT TESTS
// Put your own data below and comment out function override in the test to debug interactions with GitLab
var (
	repoUrl     = "https://gitlab.com/user/devfile-sample-go-basic"
	accessToken = "glpat-token"
)

var (
	StubEnsurePaCMergeRequest = func(g *GitlabClient, d *PaCMergeRequestData) (string, error) { return "", nil }
	StubSetupPaCWebhook       = func(g *GitlabClient, projectPath, webhookUrl, webhookSecret string) error { return nil }
)

func TestEnsurePaCMergeRequest(t *testing.T) {
	EnsurePaCMergeRequest = StubEnsurePaCMergeRequest

	glclient, err := NewGitlabClient(accessToken)
	if err != nil {
		t.Fatal(err)
	}

	pipelineOnPush := []byte("pipelineOnPush:\n  bundle: 'test-bundle-1'\n  when: 'on-push'\n")
	pipelineOnPR := []byte("pipelineOnMR:\n  bundle: 'test-bundle-2'\n  when: 'on-mr'\n")

	componentName := "unittest-component-name"
	gitSourceUrlParts := strings.Split(repoUrl, "/")
	mrData := &PaCMergeRequestData{
		ProjectPath:   gitSourceUrlParts[3] + "/" + gitSourceUrlParts[4],
		CommitMessage: "Appstudio update " + componentName,
		Branch:        "appstudio-" + componentName,
		BaseBranch:    "main",
		MrTitle:       "Appstudio update " + componentName,
		MrText:        "Pipelines as Code configuration proposal",
		AuthorName:    "redhat-appstudio",
		AuthorEmail:   "appstudio@redhat.com",
		Files: []File{
			{FullPath: ".tekton/" + componentName + "-push.yaml", Content: pipelineOnPush},
			{FullPath: ".tekton/" + componentName + "-merge-request.yaml", Content: pipelineOnPR},
		},
	}

	url, err := EnsurePaCMergeRequest(glclient, mrData)
	if err != nil {
		t.Fatal(err)
	}
	if url != "" && !strings.HasPrefix(url, "http") {
		t.Fatal("Merge Request URL must not be empty")
	}
}

func TestSetupPaCWebhook(t *testing.T) {
	SetupPaCWebhook = StubSetupPaCWebhook

	targetWebhookUrl := "https://pac.route.my-cluster.net"
	webhookSecretString := "d01b38971dad59514298d763f288392c08221043"

	glclient, err := NewGitlabClient(accessToken)
	if err != nil {
		t.Fatal(err)
	}

	gitSourceUrlParts := strings.Split(repoUrl, "/")
	projectPath := gitSourceUrlParts[3] + "/" + gitSourceUrlParts[4]

	err = SetupPaCWebhook(glclient, projectPath, targetWebhookUrl, webhookSecretString)
	if err != nil {
		t.Fatal(err)
	}
}
