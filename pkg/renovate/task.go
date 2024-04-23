package renovate

import (
	"context"

	"github.com/redhat-appstudio/build-service/pkg/git"
	"github.com/redhat-appstudio/build-service/pkg/git/credentials"
)

// Task represents a task to be executed by Renovate with credentials and repositories
type Task struct {
	Platform        string
	Username        string
	GitAuthor       string
	RenovatePattern string
	Token           string
	Repositories    []*Repository
}

// AddNewBranchToTheExistedRepositoryTasksOnTheSameHosts iterates over the tasks and adds a new branch to the repository if it already exists
// NOTE: it is unsafe to perform this operation on a slice that cont
func AddNewBranchToTheExistedRepositoryTasksOnTheSameHosts(tasks []*Task, component *git.ScmComponent) bool {
	for _, t := range tasks {
		for _, r := range t.Repositories {
			if r.Repository == component.Repository() {
				r.AddBranch(component.Branch())
				return true
			}
		}
	}
	return false
}

func AddNewRepoToTasksOnTheSameHostsWithSameCredentials(tasks []*Task, component *git.ScmComponent, cred *credentials.BasicAuthCredentials) bool {
	for _, t := range tasks {
		if t.Token == cred.Password && t.Username == cred.Username {
			//double check if the repository is already added
			for _, r := range t.Repositories {
				if r.Repository == component.Repository() {
					return true
				}
			}
			t.Repositories = append(t.Repositories, &Repository{
				Repository:   component.Repository(),
				BaseBranches: []string{component.Branch()},
			})
			return true
		}
	}
	return false
}

// TaskProvider is an interface for providing tasks to be executed by Renovate
type TaskProvider interface {
	GetNewTasks(ctx context.Context, components []*git.ScmComponent) []*Task
}

func (t *Task) JobConfig() JobConfig {
	return NewTektonJobConfig(t.Platform, t.Username, t.GitAuthor, t.RenovatePattern, t.Repositories)
}
