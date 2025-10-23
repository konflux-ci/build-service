/*
Copyright 2022-2025 Red Hat, Inc.

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
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	ghinstallation "github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v45/github"
	"github.com/konflux-ci/build-service/pkg/boerrors"
)

// Allow mocking for tests
var NewGithubClientByApp func(appId int64, privateKeyPem []byte, repoUrl string) (*GithubClient, error) = newGithubClientByApp

var GetAppInstallationsForRepository func(githubAppIdStr string, appPrivateKeyPem []byte, repoUrl string) (*ApplicationInstallation, string, error) = getAppInstallationsForRepository

func newGithubClientByApp(appId int64, privateKeyPem []byte, repoUrl string) (*GithubClient, error) {
	owner, repository := getOwnerAndRepoFromUrl(repoUrl)

	itr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appId, privateKeyPem)
	if err != nil {
		// Inability to create transport based on a private key indicates that the key is bad formatted
		return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppMalformedPrivateKey, err)
	}
	client := github.NewClient(&http.Client{Transport: itr})

	// check if application exists
	githubApp, resp, err := client.Apps.Get(context.Background(), "")
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

	// get application installation for owner & repository
	val, resp, err := client.Apps.FindRepositoryInstallation(context.Background(), owner, repository)
	if err != nil {
		if resp != nil && resp.Response != nil && resp.Response.StatusCode != 0 {
			switch resp.StatusCode {
			case 401:
				return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppPrivateKeyNotMatched, err)
			case 404:
				return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppNotInstalled, err)
			}
		}
		return nil, boerrors.NewBuildOpError(boerrors.ETransientError, err)
	}

	token, _, err := client.Apps.CreateInstallationToken(
		context.Background(),
		*val.ID,
		&github.InstallationTokenOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "installation has been suspended") {
			return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppSuspended, err)
		} else {
			return nil, err
		}
	}

	githubClient := NewGithubClient(token.GetToken())
	githubClient.appId = appId
	githubClient.appPrivateKeyPem = privateKeyPem
	githubClient.appName = githubApp.GetName()
	githubClient.appSlug = githubApp.GetSlug()
	return githubClient, nil
}

// GetConfiguredGitAppName returns name and slug of GitHub App that created client token.
func (g *GithubClient) GetConfiguredGitAppName() (string, string, error) {
	if g.isAppConfigured() {
		return g.appName, g.appSlug, nil
	} else {
		return "", "", fmt.Errorf("Github application is not configured")
	}
}

func (g *GithubClient) isAppConfigured() bool {
	return g.appId != 0 && len(g.appPrivateKeyPem) > 0
}

type ApplicationInstallation struct {
	Token        string
	ID           int64
	Repositories []*github.Repository
}

func getAppInstallationsForRepository(githubAppIdStr string, appPrivateKeyPem []byte, repoUrl string) (*ApplicationInstallation, string, error) {
	githubAppId, err := strconv.ParseInt(githubAppIdStr, 10, 64)
	if err != nil {
		return nil, "", boerrors.NewBuildOpError(boerrors.EGitHubAppMalformedId,
			fmt.Errorf("failed to convert %s to int: %w", githubAppIdStr, err))
	}

	ghUrlRegex, err := regexp.Compile(`github.com/([^/]+)/([^/]+)(\.git)?$`)
	if err != nil {
		return nil, "", err
	}

	match := ghUrlRegex.FindStringSubmatch(repoUrl)
	if match == nil {
		return nil, "", fmt.Errorf("unable to parse URL %s as it is not a github repo", repoUrl)
	}
	owner := match[1]
	repo := match[2]
	itr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, githubAppId, appPrivateKeyPem)
	if err != nil {
		// Inability to create transport based on a private key indicates that the key is bad formatted
		return nil, "", boerrors.NewBuildOpError(boerrors.EGitHubAppMalformedPrivateKey, err)
	}
	client := github.NewClient(&http.Client{Transport: itr})
	githubApp, _, err := client.Apps.Get(context.Background(), "")
	if err != nil {
		return nil, "", fmt.Errorf("failed to load GitHub app metadata, %w", err)
	}
	slug := githubApp.GetSlug()
	val, resp, err := client.Apps.FindRepositoryInstallation(context.Background(), owner, repo)
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
	token, _, err := client.Apps.CreateInstallationToken(
		context.Background(),
		*val.ID,
		&github.InstallationTokenOptions{})
	if err != nil {
		return nil, "", err
	}
	installationClient := NewGithubClient(token.GetToken())

	repoStruct, _, err := installationClient.client.Repositories.Get(context.TODO(), owner, repo)
	if err != nil {
		return nil, "", err
	}
	// Create a new token, that is only valid for this repo
	token, _, err = client.Apps.CreateInstallationToken(
		context.Background(),
		*val.ID,
		&github.InstallationTokenOptions{RepositoryIDs: []int64{*repoStruct.ID}})

	if err != nil {
		return nil, "", err
	}
	return &ApplicationInstallation{
		Token:        token.GetToken(),
		ID:           *val.ID,
		Repositories: []*github.Repository{repoStruct},
	}, slug, nil

}
