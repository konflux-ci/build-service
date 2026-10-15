package forgejo

import (
	"fmt"
	"net/url"
	"strings"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
)

// ForgejoClient wraps the Forgejo SDK client
type ForgejoClient struct {
	client *forgejo.Client
	org    string
	// baseURL is the root URL of the Forgejo instance, without any API path segments.
	// Both API requests and git remotes are derived from it.
	baseURL *url.URL
	token   string
}

// NewForgejoClient creates a new Forgejo client
func NewForgejoClient(accessToken, baseURL, org string) (*ForgejoClient, error) {
	parsedBaseURL, err := parseForgejoBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	client, err := forgejo.NewClient(parsedBaseURL.String(), forgejo.SetToken(accessToken))
	if err != nil {
		return nil, err
	}

	return &ForgejoClient{
		client:  client,
		org:     org,
		baseURL: parsedBaseURL,
		token:   accessToken,
	}, nil
}

// parseForgejoBaseURL normalizes the configured Forgejo endpoint into the root URL of the instance:
// the scheme, host and port are preserved, while API path segments, query and fragment are dropped.
// The host is assumed to be served over https if the scheme is omitted.
func parseForgejoBaseURL(baseURL string) (*url.URL, error) {
	rawURL := strings.TrimSpace(baseURL)
	if rawURL == "" {
		return nil, fmt.Errorf("forgejo base URL is empty")
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse forgejo base URL %q: %w", baseURL, err)
	}
	if parsedURL.Host == "" {
		return nil, fmt.Errorf("forgejo base URL %q has no host", baseURL)
	}

	// Keep a path prefix, if the instance is not served from the root of the host,
	// but strip the API path, so that git remotes can be derived from the same URL.
	path := strings.TrimRight(parsedURL.Path, "/")
	path = strings.TrimSuffix(path, "/api/v1")

	return &url.URL{
		Scheme: parsedURL.Scheme,
		Host:   parsedURL.Host,
		Path:   strings.TrimRight(path, "/"),
	}, nil
}

// GetClient returns the underlying Forgejo client
func (fc *ForgejoClient) GetClient() *forgejo.Client {
	return fc.client
}

// GetOrg returns the organization name
func (fc *ForgejoClient) GetOrg() string {
	return fc.org
}
