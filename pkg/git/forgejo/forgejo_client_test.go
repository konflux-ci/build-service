/*
Copyright 2026 Red Hat, Inc.

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

package forgejo

import (
	"fmt"
	"testing"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
)

func TestGetBrowseRepositoryAtShaLink(t *testing.T) {
	baseUrl := "https://forgejo.example.com"

	// "{{revision}}" is used in the pipeline generation
	revisionArgument := "{{revision}}"

	tests := []struct {
		name    string
		repoUrl string
		sha     string
		want    string
	}{
		{
			name:    "Basic repository",
			repoUrl: fmt.Sprintf("%s/owner/repo", baseUrl),
			sha:     revisionArgument,
			want:    fmt.Sprintf("%s/owner/repo/commit/%s", baseUrl, revisionArgument),
		},
		{
			name:    "Repository with .git suffix",
			repoUrl: fmt.Sprintf("%s/owner/repo.git", baseUrl),
			sha:     revisionArgument,
			want:    fmt.Sprintf("%s/owner/repo/commit/%s", baseUrl, revisionArgument),
		},
		{
			name:    "Specific commit SHA",
			repoUrl: fmt.Sprintf("%s/owner/repo", baseUrl),
			sha:     "abc123def456",
			want:    fmt.Sprintf("%s/owner/repo/commit/abc123def456", baseUrl),
		},
		{
			name:    "Repository in organization",
			repoUrl: fmt.Sprintf("%s/org/project/repo", baseUrl),
			sha:     revisionArgument,
			want:    fmt.Sprintf("%s/org/project/commit/%s", baseUrl, revisionArgument),
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gClient.GetBrowseRepositoryAtShaLink(tt.repoUrl, tt.sha)
			if result != tt.want {
				t.Errorf("GetBrowseRepositoryAtShaLink() = %v, want %v", result, tt.want)
			}
		})
	}
}

func TestGetBrowseRepositoryAtShaLinkInvalidUrl(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		sha     string
	}{
		{
			name:    "Invalid base URL",
			repoUrl: "http!!://invalid",
			sha:     "abc123",
		},
		{
			name:    "Invalid repo path",
			repoUrl: "https://forgejo.example.com/",
			sha:     "abc123",
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gClient.GetBrowseRepositoryAtShaLink(tt.repoUrl, tt.sha)
			if result != "" {
				t.Errorf("GetBrowseRepositoryAtShaLink() should return empty string for invalid URL, got %v", result)
			}
		})
	}
}

func TestNewForgejoClient(t *testing.T) {
	tests := []struct {
		name        string
		accessToken string
		baseUrl     string
		wantErr     bool
	}{
		{
			name:        "Invalid URL",
			accessToken: "valid-token",
			baseUrl:     "::invalid::",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewForgejoClient(tt.accessToken, tt.baseUrl)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewForgejoClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && client == nil {
				t.Error("NewForgejoClient() returned nil client")
			}
		})
	}
}

func TestNewForgejoClientWithBasicAuth(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		baseUrl  string
		wantErr  bool
	}{
		{
			name:     "Invalid URL",
			username: "user",
			password: "pass",
			baseUrl:  "::invalid::",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewForgejoClientWithBasicAuth(tt.username, tt.password, tt.baseUrl)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewForgejoClientWithBasicAuth() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && client == nil {
				t.Error("NewForgejoClientWithBasicAuth() returned nil client")
			}
		})
	}
}

// TestNotImplementedMethods was removed because all methods are now implemented in Phase 2

func TestGetConfiguredGitAppName(t *testing.T) {
	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	name, slug, _, err := gClient.GetConfiguredGitAppName()
	if err == nil {
		t.Error("GetConfiguredGitAppName() should return an error")
	}
	if name != "" {
		t.Errorf("GetConfiguredGitAppName() name = %v, want empty string", name)
	}
	if slug != "" {
		t.Errorf("GetConfiguredGitAppName() slug = %v, want empty string", slug)
	}

	if buildOpErr, ok := err.(*boerrors.BuildOpError); ok {
		if buildOpErr.GetErrorId() != int(boerrors.EForgejoGitAppNotSupported) {
			t.Errorf("GetConfiguredGitAppName() error code = %v, want %v", buildOpErr.GetErrorId(), int(boerrors.EForgejoGitAppNotSupported))
		}
	} else {
		t.Errorf("GetConfiguredGitAppName() should return BuildOpError, got %T", err)
	}
}

func TestGetAppUserId(t *testing.T) {
	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	userId, err := gClient.GetAppUserId("username")
	if err == nil {
		t.Error("GetAppUserId() should return an error")
	}
	if userId != 0 {
		t.Errorf("GetAppUserId() userId = %v, want 0", userId)
	}

	if buildOpErr, ok := err.(*boerrors.BuildOpError); ok {
		if buildOpErr.GetErrorId() != int(boerrors.EForgejoGitAppNotSupported) {
			t.Errorf("GetAppUserId() error code = %v, want %v", buildOpErr.GetErrorId(), int(boerrors.EForgejoGitAppNotSupported))
		}
	} else {
		t.Errorf("GetAppUserId() should return BuildOpError, got %T", err)
	}
}

func TestEnsurePaCMergeRequest(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		wantErr bool
	}{
		{
			name:    "Invalid repository URL",
			repoUrl: "invalid-url",
			wantErr: true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &gp.MergeRequestData{
				BranchName:     "test-branch",
				BaseBranchName: "main",
				Title:          "Test PR",
				Text:           "Test PR body",
				CommitMessage:  "Test commit",
				AuthorName:     "Test Author",
				AuthorEmail:    "test@example.com",
			}
			_, err := gClient.EnsurePaCMergeRequest(tt.repoUrl, d)
			if (err != nil) != tt.wantErr {
				t.Errorf("EnsurePaCMergeRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUndoPaCMergeRequest(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		wantErr bool
	}{
		{
			name:    "Invalid repository URL",
			repoUrl: "invalid-url",
			wantErr: true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &gp.MergeRequestData{
				BranchName:     "test-branch",
				BaseBranchName: "main",
				Title:          "Remove PaC",
				Text:           "Remove PaC configuration",
				CommitMessage:  "Remove PaC",
				AuthorName:     "Test Author",
				AuthorEmail:    "test@example.com",
			}
			_, err := gClient.UndoPaCMergeRequest(tt.repoUrl, d)
			if (err != nil) != tt.wantErr {
				t.Errorf("UndoPaCMergeRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFindUnmergedPaCMergeRequest(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		wantErr bool
	}{
		{
			name:    "Invalid repository URL",
			repoUrl: "invalid-url",
			wantErr: true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &gp.MergeRequestData{
				BranchName:     "test-branch",
				BaseBranchName: "main",
			}
			_, err := gClient.FindUnmergedPaCMergeRequest(tt.repoUrl, d)
			if (err != nil) != tt.wantErr {
				t.Errorf("FindUnmergedPaCMergeRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSetupPaCWebhook(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		wantErr bool
	}{
		{
			name:    "Invalid repository URL",
			repoUrl: "invalid-url",
			wantErr: true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gClient.SetupPaCWebhook(tt.repoUrl, "https://webhook.example.com", "secret")
			if (err != nil) != tt.wantErr {
				t.Errorf("SetupPaCWebhook() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeletePaCWebhook(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		wantErr bool
	}{
		{
			name:    "Invalid repository URL",
			repoUrl: "invalid-url",
			wantErr: true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gClient.DeletePaCWebhook(tt.repoUrl, "https://webhook.example.com")
			if (err != nil) != tt.wantErr {
				t.Errorf("DeletePaCWebhook() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetBranchSha(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		branch  string
		wantErr bool
	}{
		{
			name:    "Invalid repository URL",
			repoUrl: "invalid-url",
			branch:  "main",
			wantErr: true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gClient.GetBranchSha(tt.repoUrl, tt.branch)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetBranchSha() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeleteBranch(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		branch  string
		wantErr bool
	}{
		{
			name:    "Invalid repository URL",
			repoUrl: "invalid-url",
			branch:  "feature-branch",
			wantErr: true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gClient.DeleteBranch(tt.repoUrl, tt.branch)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteBranch() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetDefaultBranchWithChecks(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		wantErr bool
	}{
		{
			name:    "Invalid repository URL",
			repoUrl: "invalid-url",
			wantErr: true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gClient.GetDefaultBranchWithChecks(tt.repoUrl)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetDefaultBranchWithChecks() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDownloadFileContent(t *testing.T) {
	tests := []struct {
		name     string
		repoUrl  string
		branch   string
		filePath string
		wantErr  bool
	}{
		{
			name:     "Invalid repository URL",
			repoUrl:  "invalid-url",
			branch:   "main",
			filePath: "README.md",
			wantErr:  true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gClient.DownloadFileContent(tt.repoUrl, tt.branch, tt.filePath)
			if (err != nil) != tt.wantErr {
				t.Errorf("DownloadFileContent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsFileExist(t *testing.T) {
	tests := []struct {
		name     string
		repoUrl  string
		branch   string
		filePath string
		wantErr  bool
	}{
		{
			name:     "Invalid repository URL",
			repoUrl:  "invalid-url",
			branch:   "main",
			filePath: "README.md",
			wantErr:  true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gClient.IsFileExist(tt.repoUrl, tt.branch, tt.filePath)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsFileExist() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsRepositoryPublic(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		wantErr bool
	}{
		{
			name:    "Invalid repository URL",
			repoUrl: "invalid-url",
			wantErr: true,
		},
	}

	// Create a mock client to avoid DNS lookup
	gClient := &ForgejoClient{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gClient.IsRepositoryPublic(tt.repoUrl)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsRepositoryPublic() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
