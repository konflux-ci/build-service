package git

import "fmt"

// Endpoint interface defines the method to get the endpoint.
type Endpoint interface {
	GetEndpoint(host string) string
}

// GithubEndpoint represents an endpoint for GitHub.
type GithubEndpoint struct {
}

// GetEndpoint returns the GitHub endpoint.
func (g *GithubEndpoint) GetEndpoint(host string) string {
	return fmt.Sprintf("https://%s/api/v3/", host)
}

// GitlabEndpoint represents an endpoint for GitLab.
type GitlabEndpoint struct {
}

// GetEndpoint returns the GitLab endpoint.
func (g *GitlabEndpoint) GetEndpoint(host string) string {
	return fmt.Sprintf("https://%s/api/v4/", host)
}

// UnknownEndpoint represents an endpoint for GitLab.
type UnknownEndpoint struct {
}

// GetEndpoint returns the GitLab endpoint.
func (g *UnknownEndpoint) GetEndpoint(host string) string {
	return ""
}

// BuildEndpoint constructs and returns an endpoint object based on the type provided type.
func BuildEndpoint(endpointType string) Endpoint {
	switch endpointType {
	case "github":
		return &GithubEndpoint{}
	case "gitlab":
		return &GitlabEndpoint{}
	default:
		return &UnknownEndpoint{}
	}
}
