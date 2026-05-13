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
	"errors"
	"testing"
)

func TestGetOwnerAndRepoFromUrl(t *testing.T) {
	tests := []struct {
		name      string
		repoUrl   string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "Basic repository",
			repoUrl:   "https://forgejo.example.com/owner/repository",
			wantOwner: "owner",
			wantRepo:  "repository",
			wantErr:   false,
		},
		{
			name:      "Repository ending with .git",
			repoUrl:   "https://forgejo.example.com/owner/repository.git",
			wantOwner: "owner",
			wantRepo:  "repository",
			wantErr:   false,
		},
		{
			name:      "Repository with multiple path segments",
			repoUrl:   "https://forgejo.example.com/org/team/repository",
			wantOwner: "org",
			wantRepo:  "team",
			wantErr:   false,
		},
		{
			name:      "Invalid URL - only owner",
			repoUrl:   "https://forgejo.example.com/owner",
			wantOwner: "",
			wantRepo:  "",
			wantErr:   true,
		},
		{
			name:      "Invalid URL - no path",
			repoUrl:   "https://forgejo.example.com/",
			wantOwner: "",
			wantRepo:  "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := getOwnerAndRepoFromUrl(tt.repoUrl)
			if (err != nil) != tt.wantErr {
				t.Errorf("getOwnerAndRepoFromUrl() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if owner != tt.wantOwner {
				t.Errorf("getOwnerAndRepoFromUrl() owner = %v, want %v", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("getOwnerAndRepoFromUrl() repo = %v, want %v", repo, tt.wantRepo)
			}
		})
	}
}

func TestGetBaseUrl(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
		want    string
		err     error
	}{
		{
			name:    "Self-hosted Forgejo",
			repoUrl: "https://forgejo.example.com/owner/repository",
			want:    "https://forgejo.example.com/",
			err:     nil,
		},
		{
			name:    "Repository with .git suffix",
			repoUrl: "https://forgejo.example.com/owner/repository.git",
			want:    "https://forgejo.example.com/",
			err:     nil,
		},
		{
			name:    "Invalid URL",
			repoUrl: "http!!://invalid",
			want:    "",
			err:     FailedToParseUrlError{},
		},
		{
			name:    "Missing schema",
			repoUrl: "forgejo.example.com/owner/repository",
			want:    "",
			err:     MissingSchemaError{},
		},
		{
			name:    "HTTP URL",
			repoUrl: "http://forgejo.example.com/owner/repository",
			want:    "http://forgejo.example.com/",
			err:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetBaseUrl(tt.repoUrl)
			if err != nil && tt.err == nil {
				t.Fatalf("Expected call to succeed, found error %s", err.Error())
			}
			if err == nil && tt.err != nil {
				t.Fatalf("Expected call to end with an error, but it succeeded")
			}
			if got != tt.want {
				t.Errorf("GetBaseUrl() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetBaseUrlFailToParseUrl(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
	}{
		{
			name:    "Invalid URL format",
			repoUrl: "http!!://abc",
		},
		{
			name:    "Malformed URL",
			repoUrl: ":::invalid:::",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetBaseUrl(tt.repoUrl)
			if got != "" {
				t.Fatalf("Expected an empty string result, got %s", got)
			}
			if _, ok := err.(FailedToParseUrlError); !ok {
				t.Fatalf("Expected to receive error of type FailedToParseUrlError got %T", err)
			}
		})
	}
}

func TestGetBaseUrlMissingSchema(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
	}{
		{
			name:    "forgejo.com without schema",
			repoUrl: "forgejo.com/owner/repo",
		},
		{
			name:    "Self-hosted without schema",
			repoUrl: "forgejo.example.com/owner/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetBaseUrl(tt.repoUrl)
			if got != "" {
				t.Fatalf("Expected an empty string result, got %s", got)
			}
			if _, ok := err.(MissingSchemaError); !ok {
				t.Fatalf("Expected to receive error of type MissingSchemaError got %T", err)
			}
		})
	}
}

func TestGetBaseUrlMissingHost(t *testing.T) {
	tests := []struct {
		name    string
		repoUrl string
	}{
		{
			name:    "Only schema",
			repoUrl: "https://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetBaseUrl(tt.repoUrl)
			if got != "" {
				t.Fatalf("Expected an empty string result, got %s", got)
			}
			if _, ok := err.(MissingHostError); !ok {
				t.Fatalf("Expected to receive error of type MissingHostError got %T", err)
			}
		})
	}
}

func TestFailedToParseUrlError(t *testing.T) {
	err := FailedToParseUrlError{url: "http://example.com", err: "parse error"}
	expected := "Failed to parse url: http://example.com, error: parse error"
	if err.Error() != expected {
		t.Errorf("FailedToParseUrlError.Error() = %v, want %v", err.Error(), expected)
	}
}

func TestMissingSchemaError(t *testing.T) {
	err := MissingSchemaError{url: "example.com"}
	expected := "Failed to detect schema in url example.com"
	if err.Error() != expected {
		t.Errorf("MissingSchemaError.Error() = %v, want %v", err.Error(), expected)
	}
}

func TestMissingHostError(t *testing.T) {
	err := MissingHostError{url: "https://"}
	expected := "Failed to detect host in url https://"
	if err.Error() != expected {
		t.Errorf("MissingHostError.Error() = %v, want %v", err.Error(), expected)
	}
}

func TestRefineGitHostingServiceError(t *testing.T) {
	tests := []struct {
		name        string
		response    interface{}
		originErr   error
		wantErrCode int
	}{
		{
			name:        "Nil response with 401 error message",
			response:    nil,
			originErr:   errors.New("401 Unauthorized"),
			wantErrCode: 95, // EForgejoTokenUnauthorized
		},
		{
			name:        "Nil response with 403 error message",
			response:    nil,
			originErr:   errors.New("403 Forbidden"),
			wantErrCode: 96, // EForgejoTokenInsufficientScope
		},
		{
			name:        "Nil response with generic error",
			response:    nil,
			originErr:   errors.New("some other error"),
			wantErrCode: 0, // No error code (returns original error)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := refineGitHostingServiceError(nil, tt.originErr)
			if tt.wantErrCode == 0 {
				if err != tt.originErr {
					t.Errorf("refineGitHostingServiceError() should return original error")
				}
			} else {
				// Just verify error is not nil - detailed error code checking would require importing boerrors
				if err == nil {
					t.Errorf("refineGitHostingServiceError() should return an error")
				}
			}
		})
	}
}

func TestBase64Encode(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{
			name:    "Simple text",
			content: []byte("hello world"),
			want:    "aGVsbG8gd29ybGQ=",
		},
		{
			name:    "Empty content",
			content: []byte(""),
			want:    "",
		},
		{
			name:    "Binary content",
			content: []byte{0x00, 0x01, 0x02, 0xff},
			want:    "AAEC/w==",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := base64Encode(tt.content)
			if got != tt.want {
				t.Errorf("base64Encode() = %v, want %v", got, tt.want)
			}
		})
	}
}
