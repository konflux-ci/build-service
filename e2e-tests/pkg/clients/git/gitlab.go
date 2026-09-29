package git

import (
	"encoding/base64"
	"fmt"
	"strings"

	goGitlab "github.com/xanzy/go-gitlab"

	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/gitlab"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/constants"
)

type GitLabClient struct {
	*gitlab.GitlabClient
}

func NewGitlabClient(gl *gitlab.GitlabClient) *GitLabClient {
	return &GitLabClient{gl}
}

func (g *GitLabClient) CreateBranch(repository, baseBranchName, _, branchName string) error {
	return g.GitlabClient.CreateBranch(repository, branchName, baseBranchName)
}

func (g *GitLabClient) BranchExists(repository, branchName string) (bool, error) {
	return g.ExistsBranch(repository, branchName)
}

func (g *GitLabClient) ListPullRequests(projectId string) ([]*PullRequest, error) {
	mrs, err := g.GetMergeRequests(projectId)
	if err != nil {
		return nil, err
	}
	var pullRequests []*PullRequest
	for _, mr := range mrs {
		pullRequests = append(pullRequests, &PullRequest{
			Number:       mr.IID,
			SourceBranch: mr.SourceBranch,
			TargetBranch: mr.TargetBranch,
			HeadSHA:      mr.SHA,
		})
	}
	return pullRequests, nil
}

func (g *GitLabClient) CreateFile(repository, pathToFile, content, branchName string) (*RepositoryFile, error) {
	_, err := g.GitlabClient.CreateFile(repository, pathToFile, content, branchName)
	if err != nil {
		return nil, err
	}

	opts := goGitlab.GetFileOptions{Ref: goGitlab.Ptr(branchName)}
	file, _, err := g.GetClient().RepositoryFiles.GetFile(repository, pathToFile, &opts)
	if err != nil {
		return nil, err
	}

	resultFile := &RepositoryFile{
		CommitSHA: file.CommitID,
	}
	return resultFile, nil
}

func (g *GitLabClient) GetFile(repository, pathToFile, branchName string) (*RepositoryFile, error) {
	opts := goGitlab.GetFileOptions{Ref: goGitlab.Ptr(branchName)}
	file, _, err := g.GetClient().RepositoryFiles.GetFile(repository, pathToFile, &opts)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil {
		return nil, err
	}
	resultFile := &RepositoryFile{
		CommitSHA: file.CommitID,
		Content:   string(decoded),
	}
	return resultFile, nil
}

func (g *GitLabClient) MergePullRequest(repository string, prNumber int) (*PullRequest, error) {
	mr, err := g.AcceptMergeRequest(repository, prNumber)
	if err != nil {
		return nil, err
	}
	return &PullRequest{
		Number:         mr.IID,
		SourceBranch:   mr.SourceBranch,
		TargetBranch:   mr.TargetBranch,
		HeadSHA:        mr.SHA,
		MergeCommitSHA: mr.MergeCommitSHA,
	}, nil
}

func (g *GitLabClient) UpdatePullRequestBranch(repository string, prNumber int) error {
	// GitLab handles MR branch updates via rebase
	opts := goGitlab.RebaseMergeRequestOptions{}
	_, err := g.GetClient().MergeRequests.RebaseMergeRequest(repository, prNumber, &opts)
	return err
}

func (g *GitLabClient) CreatePullRequest(repository, title, body, head, base string) (*PullRequest, error) {
	opts := goGitlab.CreateMergeRequestOptions{
		Title:        goGitlab.Ptr(title),
		Description:  goGitlab.Ptr(body),
		SourceBranch: goGitlab.Ptr(head),
		TargetBranch: goGitlab.Ptr(base),
	}
	mr, _, err := g.GetClient().MergeRequests.CreateMergeRequest(repository, &opts)
	if err != nil {
		return nil, err
	}
	return &PullRequest{
		Number:       mr.IID,
		SourceBranch: mr.SourceBranch,
		TargetBranch: mr.TargetBranch,
		HeadSHA:      mr.SHA,
	}, nil
}

func (g *GitLabClient) CleanupWebhooks(repository, clusterAppDomain string) error {
	projectId := constants.GetGitLabProjectId(repository)
	return g.DeleteWebhooks(projectId, clusterAppDomain)
}

func (g *GitLabClient) DeleteBranchAndClosePullRequest(repository string, prNumber int) error {
	mr, _, err := g.GetClient().MergeRequests.GetMergeRequest(repository, prNumber, nil)
	if err != nil {
		return err
	}
	err = g.DeleteBranch(repository, mr.SourceBranch)
	if err != nil {
		return err
	}
	return g.CloseMergeRequest(repository, prNumber)
}

func (g *GitLabClient) ForkRepository(sourceRepoName, targetRepoName string) error {
	sourceComponents := strings.SplitN(sourceRepoName, "/", 2)
	targetComponents := strings.SplitN(targetRepoName, "/", 2)
	if len(sourceComponents) != 2 || sourceComponents[0] == "" || sourceComponents[1] == "" {
		return fmt.Errorf("source repo name must be \"org/name\", got %q", sourceRepoName)
	}
	if len(targetComponents) != 2 || targetComponents[0] == "" || targetComponents[1] == "" {
		return fmt.Errorf("target repo name must be \"org/name\", got %q", targetRepoName)
	}
	_, err := g.GitlabClient.ForkRepository(sourceComponents[0], sourceComponents[1], targetComponents[0], targetComponents[1])
	if err != nil {
		return err
	}
	return nil
}

func (g *GitLabClient) DeleteRepositoryIfExists(repoName string) error {
	err := g.DeleteRepositoryOnlyIfExists(repoName)
	if err != nil {
		return err
	}
	return nil
}
