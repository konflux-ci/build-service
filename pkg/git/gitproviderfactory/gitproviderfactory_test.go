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

package gitproviderfactory

import (
	"fmt"
	"testing"

	. "github.com/konflux-ci/build-service/pkg/common"
	"github.com/konflux-ci/build-service/pkg/git/github"
	"github.com/konflux-ci/build-service/pkg/git/gitlab"
)

func TestGetContainerImageRepository(t *testing.T) {
	denyAllConstructors := func() {
		github.NewGithubClientByApp = func(appId int64, privateKeyPem []byte, repoUrl string) (*github.GithubClient, error) {
			t.Errorf("should not be invoked")
			return nil, nil
		}
		github.NewGithubClient = func(accessToken string) *github.GithubClient {
			t.Errorf("should not be invoked")
			return nil
		}
		github.NewGithubClientWithBasicAuth = func(username, password string) *github.GithubClient {
			t.Errorf("should not be invoked")
			return nil
		}
		gitlab.NewGitlabClient = func(accessToken, baseUrl string) (*gitlab.GitlabClient, error) {
			t.Errorf("should not be invoked")
			return nil, nil
		}
		gitlab.NewGitlabClientWithBasicAuth = func(username, password, baseUrl string) (*gitlab.GitlabClient, error) {
			t.Errorf("should not be invoked")
			return nil, nil
		}
	}

	repoUrl := "https://github.com/org/repository"

	tests := []struct {
		name              string
		gitClientConfig   GitClientConfig
		allowConstructors func()
		expectError       bool
	}{
		{
			name: "should create GitHub client from app",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte("private key"),
				},
				GitProvider: "github",
				RepoUrl:     repoUrl,
			},
			allowConstructors: func() {
				github.NewGithubClientByApp = func(appId int64, privateKeyPem []byte, repoUrl string) (*github.GithubClient, error) {
					return &github.GithubClient{}, nil
				}
			},
			expectError: false,
		},
		{
			name: "should not create GitHub client from app if the app is not installed into repository",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte("private key"),
				},
				GitProvider: "github",
				RepoUrl:     repoUrl,
			},
			allowConstructors: func() {
				github.NewGithubClientByApp = func(appId int64, privateKeyPem []byte, repoUrl string) (*github.GithubClient, error) {
					return nil, fmt.Errorf("application isn't installed")
				}

			},
			expectError: true,
		},
		{
			name: "should not create GitHub client from app if app id is not a number",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12abcd"),
					PipelinesAsCodeGithubPrivateKey: []byte("private key"),
				},
				GitProvider: "github",
				RepoUrl:     repoUrl,
			},
			allowConstructors: func() {},
			expectError:       true,
		},
		{
			name: "should not create GitHub client from app if app id and private key mismatch or invalid",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte("private key"),
				},
				GitProvider: "github",
				RepoUrl:     repoUrl,
			},
			allowConstructors: func() {
				github.NewGithubClientByApp = func(appId int64, privateKeyPem []byte, repoUrl string) (*github.GithubClient, error) {
					return nil, fmt.Errorf("wrong key")
				}
			},
			expectError: true,
		},
		{
			name: "should create GitHub client from token",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					"password": []byte("token"),
				},
				GitProvider: "github",
				RepoUrl:     repoUrl,
			},
			allowConstructors: func() {
				github.NewGithubClient = func(accessToken string) *github.GithubClient {
					expectedToken := "token"
					if accessToken != expectedToken {
						t.Errorf("expected token %s, got %s", expectedToken, accessToken)
					}
					return &github.GithubClient{}
				}
			},
			expectError: false,
		},
		{
			name: "should create GitHub client from username and password",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					"username": []byte("user"),
					"password": []byte("pass"),
				},
				GitProvider: "github",
				RepoUrl:     repoUrl,
			},
			allowConstructors: func() {
				github.NewGithubClientWithBasicAuth = func(username, password string) *github.GithubClient {
					return &github.GithubClient{}
				}
			},
			expectError: false,
		},
		{
			name: "should create GitLab client from token",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					"password": []byte("token"),
				},
				GitProvider: "gitlab",
				RepoUrl:     "https://gitlab.com/my-org/my-repo",
			},
			allowConstructors: func() {
				gitlab.NewGitlabClient = func(accessToken, baseUrl string) (*gitlab.GitlabClient, error) {
					expectedBaseUrl := "https://gitlab.com/"
					expectedToken := "token"
					if baseUrl != expectedBaseUrl {
						return nil, fmt.Errorf("Expected to get baseUrl: %s, got %s", expectedBaseUrl, baseUrl)
					}
					if accessToken != expectedToken {
						return nil, fmt.Errorf("Expected to get token: %s, got %s", expectedToken, accessToken)
					}
					return &gitlab.GitlabClient{}, nil
				}
			},
			expectError: false,
		},
		{
			name: "should create GitLab client from username and password",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					"username": []byte("user"),
					"password": []byte("pass"),
				},
				GitProvider: "gitlab",
				RepoUrl:     "https://gitlab.com/my-org/my-repo",
			},
			allowConstructors: func() {
				gitlab.NewGitlabClientWithBasicAuth = func(username, password, baseUrl string) (*gitlab.GitlabClient, error) {
					expectedBaseUrl := "https://gitlab.com/"
					if baseUrl != expectedBaseUrl {
						return nil, fmt.Errorf("Expected to get baseUrl: %s, got %s", expectedBaseUrl, baseUrl)
					}
					return &gitlab.GitlabClient{}, nil
				}
			},
			expectError: false,
		},
		{
			name: "should fail to create Gitlab client since the base url can't be detected",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					"gitlab_token": []byte("token"),
				},
				GitProvider: "gitlab",
				RepoUrl:     "https://",
			},
			allowConstructors: func() {},
			expectError:       true,
		},
		{
			name: "should not create BitBucket client",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					"bitbucket_token": []byte("token"),
				},
				GitProvider: "bitbucket",
				RepoUrl:     repoUrl,
			},
			allowConstructors: func() {
			},
			expectError: true,
		},
		{
			name: "should not create unknown client",
			gitClientConfig: GitClientConfig{
				PacSecretData: map[string][]byte{
					"unknonw_token": []byte("token"),
				},
				GitProvider: "unknown",
				RepoUrl:     repoUrl,
			},
			allowConstructors: func() {
			},
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			denyAllConstructors()
			tt.allowConstructors()

			gitClient, err := createGitClient(tt.gitClientConfig)

			if err != nil {
				if !tt.expectError {
					t.Errorf("failed to create git client from config: %#v", tt.gitClientConfig)
				}
				return
			} else {
				if tt.expectError {
					t.Errorf("expected error in git client creation for git config: %#v", tt.gitClientConfig)
				}
			}

			if gitClient == nil {
				t.Errorf("git clinet is nil")
			}
		})
	}
}
