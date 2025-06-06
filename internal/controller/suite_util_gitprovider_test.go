/*
Copyright 2023-2025 Red Hat, Inc.

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
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
	gpf "github.com/konflux-ci/build-service/pkg/git/gitproviderfactory"
)

var (
	testGitProviderClient   = &TestGitProviderClient{}
	DefaultBrowseRepository = "https://githost.com/user/repo?rev="
	UndoPacMergeRequestURL  = "https://githost.com/mr/5678"
	TestGitHubAppName       = "test-github-app"
	TestGitHubAppId         = int64(1234567890)

	EnsurePaCMergeRequestFunc        func(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error)
	UndoPaCMergeRequestFunc          func(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error)
	FindUnmergedPaCMergeRequestFunc  func(repoUrl string, data *gp.MergeRequestData) (*gp.MergeRequest, error)
	SetupPaCWebhookFunc              func(repoUrl string, webhookUrl string, webhookSecret string) error
	DeletePaCWebhookFunc             func(repoUrl string, webhookUrl string) error
	GetDefaultBranchWithChecksFunc   func(repoUrl string) (string, error)
	DeleteBranchFunc                 func(repoUrl string, branchName string) (bool, error)
	GetBranchShaFunc                 func(repoUrl string, branchName string) (string, error)
	GetBrowseRepositoryAtShaLinkFunc func(repoUrl string, sha string) string
	DownloadFileContentFunc          func(repoUrl, branchName, filePath string) ([]byte, error)
	IsFileExistFunc                  func(repoUrl, branchName, filePath string) (bool, error)
	IsRepositoryPublicFunc           func(repoUrl string) (bool, error)
	GetConfiguredGitAppNameFunc      func() (string, string, error)
	GetAppUserIdFunc                 func(userName string) (int64, error)
)

func ResetTestGitProviderClient() {
	gpf.CreateGitClient = func(gitClientConfig gpf.GitClientConfig) (gp.GitProviderClient, error) {
		return testGitProviderClient, nil
	}

	EnsurePaCMergeRequestFunc = func(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error) {
		return "https://githost.com/mr/1234", nil
	}
	UndoPaCMergeRequestFunc = func(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error) {
		return UndoPacMergeRequestURL, nil
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
	GetDefaultBranchWithChecksFunc = func(repoUrl string) (string, error) {
		return "dafaultbranch", nil
	}
	DeleteBranchFunc = func(repoUrl string, branchName string) (bool, error) {
		return true, nil
	}
	GetBranchShaFunc = func(repoUrl string, branchName string) (string, error) {
		return "abcd890", nil
	}
	GetBrowseRepositoryAtShaLinkFunc = func(repoUrl string, sha string) string {
		return DefaultBrowseRepository + sha
	}
	DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
		return []byte{0}, nil
	}
	IsFileExistFunc = func(repoUrl, branchName, filePath string) (bool, error) {
		return true, nil
	}
	IsRepositoryPublicFunc = func(repoUrl string) (bool, error) {
		return true, nil
	}
	GetConfiguredGitAppNameFunc = func() (string, string, error) {
		return "git-app-name", "slug", nil
	}
	GetAppUserIdFunc = func(userName string) (int64, error) {
		return TestGitHubAppId, nil
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
func (*TestGitProviderClient) GetDefaultBranchWithChecks(repoUrl string) (string, error) {
	return GetDefaultBranchWithChecksFunc(repoUrl)
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
func (*TestGitProviderClient) DownloadFileContent(repoUrl, branchName, filePath string) ([]byte, error) {
	return DownloadFileContentFunc(repoUrl, branchName, filePath)
}
func (*TestGitProviderClient) IsFileExist(repoUrl, branchName, filePath string) (bool, error) {
	return IsFileExistFunc(repoUrl, branchName, filePath)
}
func (*TestGitProviderClient) IsRepositoryPublic(repoUrl string) (bool, error) {
	return IsRepositoryPublicFunc(repoUrl)
}
func (*TestGitProviderClient) GetConfiguredGitAppName() (string, string, error) {
	return GetConfiguredGitAppNameFunc()
}
func (*TestGitProviderClient) GetAppUserId(userName string) (int64, error) {
	return GetAppUserIdFunc(userName)
}
