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

package boerrors

import (
	"fmt"
)

// BuildOpError extends standard error to:
//  1. Keep persistent / transient property of the error.
//     All errors, except ETransientErrorId considered persistent.
//  2. Have error ID to show the root cause of the error and optionally short message.
type BuildOpError struct {
	// id is used to determine if error is persistent and to know the root cause of the error
	id BOErrorId
	// typically used to log the error message along with nested errors
	err error
}

func NewBuildOpError(id BOErrorId, err error) *BuildOpError {
	return &BuildOpError{
		id:  id,
		err: err,
	}
}

func (r BuildOpError) Error() string {
	return r.err.Error()
}

// ShortError returns short message with error ID in case of persistent error or
// standard error message for transient errors.
func (r BuildOpError) ShortError() string {
	if r.id == ETransientError {
		return r.Error()
	}
	return fmt.Sprintf("%d: %s", r.id, boErrorMessages[r.id])
}

func (r BuildOpError) IsPersistent() bool {
	return r.id != ETransientError
}

type BOErrorId int

const (
	ETransientError BOErrorId = 0
	EUnknownError   BOErrorId = 1

	// 'pipelines-as-code-secret' secret doesn't exists in 'build-service' namespace nor Component's one.
	EPaCSecretNotFound BOErrorId = 90
	// Validation of 'pipelines-as-code-secret' secret failed
	EPaCSecretInvalid BOErrorId = 91

	// Happens when Component source repository is hosted on unsupported / unknown git provider.
	// For example: https://my-gitlab.com
	// If self-hosted instance of the supported git providers is used, then "git-provider" annotation must be set:
	// git-provider: gitlab
	EUnknownGitProvider BOErrorId = 100

	// Happens when configured in cluster Pipelines as Code application is not installed in Component source repository.
	// User must install the application to fix this error.
	EGitHubAppNotInstalled BOErrorId = 120
	// Bad formatted private key
	EGitHubAppInvalidPrivateKey BOErrorId = 121
	// Private key doesn't match the GitHun Application
	EGitHubAppWrongPrivateKey BOErrorId = 122
	// GitHub Application with specified ID does not exists.
	// Correct configuration in the AppStudio installation ('pipelines-as-code-secret' secret in 'build-service' namespace).
	EGitHubAppDoesNotExist BOErrorId = 123
)

var boErrorMessages = map[BOErrorId]string{
	ETransientError: "",
	EUnknownError:   "unknown error",

	EPaCSecretNotFound: "Pipelines as Code secret does not exist",
	EPaCSecretInvalid:  "Invalid Pipelines as Code secret",

	EUnknownGitProvider: "unknown git provider of the source repository",

	EGitHubAppNotInstalled:      "GitHub Application is not installed in user repository",
	EGitHubAppInvalidPrivateKey: "invalid GitHub Application private key",
	EGitHubAppWrongPrivateKey:   "GitHub Application private key does not match Application ID",
	EGitHubAppDoesNotExist:      "GitHub Application with given ID does not exist",
}
