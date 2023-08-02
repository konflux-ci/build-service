/*
Copyright 2023 Red Hat, Inc.

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

package controllers

import (
	gp "github.com/redhat-appstudio/build-service/pkg/git/gitprovider"
	gpf "github.com/redhat-appstudio/build-service/pkg/git/gitproviderfactory"
)

var (
	testGitProviderClient = &TestGitProviderClient{}

	EnsurePaCMergeRequestFunc        func(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error)
	UndoPaCMergeRequestFunc          func(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error)
	FindUnmergedPaCMergeRequestFunc  func(repoUrl string, data *gp.MergeRequestData) (*gp.MergeRequest, error)
	SetupPaCWebhookFunc              func(repoUrl string, webhookUrl string, webhookSecret string) error
	DeletePaCWebhookFunc             func(repoUrl string, webhookUrl string) error
	GetDefaultBranchFunc             func(repoUrl string) (string, error)
	DeleteBranchFunc                 func(repoUrl string, branchName string) (bool, error)
	GetBranchShaFunc                 func(repoUrl string, branchName string) (string, error)
	GetBrowseRepositoryAtShaLinkFunc func(repoUrl string, sha string) string
	IsRepositoryPublicFunc           func(repoUrl string) (bool, error)
	GetConfiguredGitAppNameFunc      func() (string, string, error)
)

func ResetTestGitProviderClient() {
	gpf.CreateGitClient = func(gitClientConfig gpf.GitClientConfig) (gp.GitProviderClient, error) {
		return testGitProviderClient, nil
	}

	EnsurePaCMergeRequestFunc = func(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error) {
		return "https://githost.com/mr/1234", nil
	}
	UndoPaCMergeRequestFunc = func(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error) {
		return "https://githost.com/mr/5678", nil
	}
	FindUnmergedPaCMergeRequestFunc = func(repoUrl string, data *gp.MergeRequestData) (*gp.MergeRequest, error) {
		return nil, nil
	}
	SetupPaCWebhookFunc = func(repoUrl string, webhookUrl string, webhookSecret string) error {
		return nil
	}
	DeletePaCWebhookFunc = func(repoUrl string, webhookUrl string) error {
		return nil
	}
	GetDefaultBranchFunc = func(repoUrl string) (string, error) {
		return "dafaultbranch", nil
	}
	DeleteBranchFunc = func(repoUrl string, branchName string) (bool, error) {
		return true, nil
	}
	GetBranchShaFunc = func(repoUrl string, branchName string) (string, error) {
		return "abcd890", nil
	}
	GetBrowseRepositoryAtShaLinkFunc = func(repoUrl string, sha string) string {
		return "https://githost.com/files?sha=" + sha
	}
	IsRepositoryPublicFunc = func(repoUrl string) (bool, error) {
		return true, nil
	}
	GetConfiguredGitAppNameFunc = func() (string, string, error) {
		return "git-app-name", "slug", nil
	}
}

var _ gp.GitProviderClient = (*TestGitProviderClient)(nil)

type TestGitProviderClient struct{}

func (*TestGitProviderClient) EnsurePaCMergeRequest(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error) {
	return EnsurePaCMergeRequestFunc(repoUrl, data)
}
func (*TestGitProviderClient) UndoPaCMergeRequest(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error) {
	return UndoPaCMergeRequestFunc(repoUrl, data)
}
func (*TestGitProviderClient) FindUnmergedPaCMergeRequest(repoUrl string, data *gp.MergeRequestData) (*gp.MergeRequest, error) {
	return FindUnmergedPaCMergeRequestFunc(repoUrl, data)
}
func (*TestGitProviderClient) SetupPaCWebhook(repoUrl string, webhookUrl string, webhookSecret string) error {
	return SetupPaCWebhookFunc(repoUrl, webhookUrl, webhookSecret)
}
func (*TestGitProviderClient) DeletePaCWebhook(repoUrl string, webhookUrl string) error {
	return DeletePaCWebhookFunc(repoUrl, webhookUrl)
}
func (*TestGitProviderClient) GetDefaultBranch(repoUrl string) (string, error) {
	return GetDefaultBranchFunc(repoUrl)
}
func (*TestGitProviderClient) DeleteBranch(repoUrl string, branchName string) (bool, error) {
	return DeleteBranchFunc(repoUrl, branchName)
}
func (*TestGitProviderClient) GetBranchSha(repoUrl string, branchName string) (string, error) {
	return GetBranchShaFunc(repoUrl, branchName)
}
func (*TestGitProviderClient) GetBrowseRepositoryAtShaLink(repoUrl string, sha string) string {
	return GetBrowseRepositoryAtShaLinkFunc(repoUrl, sha)
}
func (*TestGitProviderClient) IsRepositoryPublic(repoUrl string) (bool, error) {
	return IsRepositoryPublicFunc(repoUrl)
}
func (*TestGitProviderClient) GetConfiguredGitAppName() (string, string, error) {
	return GetConfiguredGitAppNameFunc()
}
