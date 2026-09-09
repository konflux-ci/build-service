package forgejo

import (
	"testing"
)

func TestParseForgejoBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
		wantErr  bool
	}{
		{name: "codeberg", baseURL: "https://codeberg.org", expected: "https://codeberg.org"},
		{name: "trailing slash", baseURL: "https://codeberg.org/", expected: "https://codeberg.org"},
		{name: "surrounding spaces", baseURL: "  https://codeberg.org  ", expected: "https://codeberg.org"},
		{name: "api path", baseURL: "https://codeberg.org/api/v1", expected: "https://codeberg.org"},
		{name: "api path with trailing slash", baseURL: "https://codeberg.org/api/v1/", expected: "https://codeberg.org"},
		{name: "custom host with port", baseURL: "https://forgejo.example.com:3000", expected: "https://forgejo.example.com:3000"},
		{name: "custom host with port and api path", baseURL: "https://forgejo.example.com:3000/api/v1", expected: "https://forgejo.example.com:3000"},
		{name: "http scheme", baseURL: "http://forgejo.example.com:3000", expected: "http://forgejo.example.com:3000"},
		{name: "no scheme", baseURL: "forgejo.example.com", expected: "https://forgejo.example.com"},
		{name: "path prefix", baseURL: "https://example.com/forgejo/api/v1", expected: "https://example.com/forgejo"},
		{name: "query and fragment", baseURL: "https://codeberg.org/api/v1?foo=bar#baz", expected: "https://codeberg.org"},
		{name: "empty", baseURL: "", wantErr: true},
		{name: "blank", baseURL: "   ", wantErr: true},
		{name: "no host", baseURL: "https:///api/v1", wantErr: true},
		{name: "invalid", baseURL: "https://exa mple.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedURL, err := parseForgejoBaseURL(tt.baseURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error for base URL %q, got %q", tt.baseURL, parsedURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for base URL %q: %v", tt.baseURL, err)
			}
			if parsedURL.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, parsedURL.String())
			}
		})
	}
}

func TestGitRemoteURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{name: "codeberg", baseURL: "https://codeberg.org", expected: "https://codeberg.org/my-org/my-repo.git"},
		{name: "api path", baseURL: "https://codeberg.org/api/v1", expected: "https://codeberg.org/my-org/my-repo.git"},
		{name: "custom host with port", baseURL: "http://forgejo.example.com:3000", expected: "http://forgejo.example.com:3000/my-org/my-repo.git"},
		{name: "path prefix", baseURL: "https://example.com/forgejo", expected: "https://example.com/forgejo/my-org/my-repo.git"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedURL, err := parseForgejoBaseURL(tt.baseURL)
			if err != nil {
				t.Fatalf("unexpected error for base URL %q: %v", tt.baseURL, err)
			}
			fc := &ForgejoClient{baseURL: parsedURL}

			if got := fc.gitRemoteURL("my-org", "my-repo"); got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestAPIURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		endpoint string
		expected string
	}{
		{
			name:     "codeberg",
			baseURL:  "https://codeberg.org",
			endpoint: "/repos/my-org/my-repo/branches",
			expected: "https://codeberg.org/api/v1/repos/my-org/my-repo/branches",
		},
		{
			name:     "api path is not duplicated",
			baseURL:  "https://codeberg.org/api/v1",
			endpoint: "/repos/my-org/my-repo/branches",
			expected: "https://codeberg.org/api/v1/repos/my-org/my-repo/branches",
		},
		{
			name:     "path prefix with custom port",
			baseURL:  "http://forgejo.example.com:3000/forgejo",
			endpoint: "/repos/my-org/my-repo/pulls/1/update",
			expected: "http://forgejo.example.com:3000/forgejo/api/v1/repos/my-org/my-repo/pulls/1/update",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedURL, err := parseForgejoBaseURL(tt.baseURL)
			if err != nil {
				t.Fatalf("unexpected error for base URL %q: %v", tt.baseURL, err)
			}
			fc := &ForgejoClient{baseURL: parsedURL}

			if got := fc.apiURL(tt.endpoint); got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
