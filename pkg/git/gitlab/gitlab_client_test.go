package gitlab

import (
	"fmt"
	"testing"
)

func TestGetBrowseRepositoryAtShaLink(t *testing.T) {
	baseUrl := "https://gitlab.cee.foo.com"

	// "{{revision}}" is used in the pipeline generation
	revisionArgument := "{{revision}}"

	expectedEnding := fmt.Sprintf("-/tree/%s", revisionArgument)
	testCases := []struct {
		TestName string
		RepoUrl  string
		SHA      string
		Result   string
	}{
		{
			TestName: "Basic repository",
			RepoUrl:  fmt.Sprintf("%s/group/repo", baseUrl),
			Result:   fmt.Sprintf("%s/group/repo/%s", baseUrl, expectedEnding),
		},
		{
			TestName: "Repository in a subgroup",
			RepoUrl:  fmt.Sprintf("%s/group/subgroup/repo", baseUrl),
			Result:   fmt.Sprintf("%s/group/subgroup/repo/%s", baseUrl, expectedEnding),
		},
		{
			TestName: "Repository ending with '.git'",
			RepoUrl:  fmt.Sprintf("%s/group/subgroup/repo.git", baseUrl),
			Result:   fmt.Sprintf("%s/group/subgroup/repo/%s", baseUrl, expectedEnding),
		},
	}

	glClient, err := NewGitlabClient("", baseUrl)
	if err != nil {
		t.Fatal("Failed to create Gitlab client")
	}
	for _, tc := range testCases {
		t.Run(tc.TestName, func(t *testing.T) {
			result := glClient.GetBrowseRepositoryAtShaLink(tc.RepoUrl, revisionArgument)
			if result != tc.Result {
				t.Errorf("got %s, want %s", result, tc.Result)
			}
		})
	}
}
