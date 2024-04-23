package renovate

import (
	"context"

	"github.com/redhat-appstudio/build-service/pkg/git"
)

// Task represents a task to be executed by Renovate with credentials and repositories
type Task struct {
	Platform        string
	Username        string
	GitAuthor       string
	RenovatePattern string
	Token           string
	Repositories    []Repository
}

// TaskProvider is an interface for providing tasks to be executed by Renovate
type TaskProvider interface {
	GetNewTasks(ctx context.Context, components []*git.ScmComponent) []Task
}

func (t *Task) JobConfig() JobConfig {
	return NewTektonJobConfig(t.Platform, t.Username, t.GitAuthor, t.RenovatePattern, t.Repositories)
}
