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
	"testing"
)

func TestPersistentErrorDetection(t *testing.T) {
	tests := []struct {
		name        string
		errId       BOErrorId
		nestedError error
		isPersitent bool
	}{
		{
			name:        "should treat transient error as not persistent",
			errId:       ETransientError,
			nestedError: fmt.Errorf("network error"),
			isPersitent: false,
		},
		{
			name:        "should treat PaC GH App not installed error as persistent",
			errId:       EGitHubAppNotInstalled,
			nestedError: fmt.Errorf("App with given id not found"),
			isPersitent: true,
		},
		{
			name:        "should treat any non transient error as persistent",
			errId:       EUnknownError,
			nestedError: fmt.Errorf("An error"),
			isPersitent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			boErr := NewBuildOpError(tt.errId, tt.nestedError)
			if boErr.IsPersistent() != tt.isPersitent {
				t.Errorf("Wrong error persistance")
			}

			if boErr.GetErrorId() != int(tt.errId) {
				t.Errorf("Wrong error id")
			}
		})
	}
}

func TestNestedErrorMessage(t *testing.T) {
	tests := []struct {
		name                       string
		nestedError                error
		extraInfo                  string
		expectedNestedErrorMessage string
	}{
		{
			name:                       "should return nested error message",
			nestedError:                fmt.Errorf("invalid credentials"),
			extraInfo:                  "",
			expectedNestedErrorMessage: "invalid credentials",
		},
		{
			name:                       "should return persistent error id and short error description if nested error is nil",
			nestedError:                nil,
			extraInfo:                  "",
			expectedNestedErrorMessage: "1: unknown error",
		},
		{
			name:                       "should include extra information when it's defined",
			nestedError:                fmt.Errorf("404 no resource []"),
			extraInfo:                  "permission denied",
			expectedNestedErrorMessage: "404 no resource [] permission denied",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			boErr := NewBuildOpError(EUnknownError, tt.nestedError)
			boErr.ExtraInfo = tt.extraInfo
			if boErr.Error() != tt.expectedNestedErrorMessage {
				t.Errorf("Expected \"%s\" error message, but got \"%s\"", tt.expectedNestedErrorMessage, boErr.Error())
			}
		})
	}
}

func TestShortErrorMessage(t *testing.T) {
	tests := []struct {
		name                 string
		nestedError          error
		errId                BOErrorId
		expectedShortMessage string
	}{
		{
			name:                 "should return short error message",
			nestedError:          nil,
			errId:                EGitHubAppNotInstalled,
			expectedShortMessage: "70: GitHub Application is not installed in user repository",
		},
		{
			name:                 "should return nested error's message for transient error",
			nestedError:          fmt.Errorf("http connection error"),
			errId:                ETransientError,
			expectedShortMessage: "http connection error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			boErr := NewBuildOpError(tt.errId, tt.nestedError)
			if boErr.ShortError() != tt.expectedShortMessage {
				t.Errorf("Expected \"%s\" error message, but got \"%s\"", tt.expectedShortMessage, boErr.Error())
			}
		})
	}
}
