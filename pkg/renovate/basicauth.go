package renovate

import (
	"context"
	"fmt"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/redhat-appstudio/build-service/pkg/boerrors"
	"github.com/redhat-appstudio/build-service/pkg/git"
	"github.com/redhat-appstudio/build-service/pkg/git/credentials"
	"github.com/redhat-appstudio/build-service/pkg/logs"
)

// BasicAuthTaskProvider is an implementation of the renovate.TaskProvider that creates the renovate.Task for the components
// based on the generic algorithm and not tied to any specific SCM provider implementation.
type BasicAuthTaskProvider struct {
	credentialsProvider credentials.BasicAuthCredentialsProvider
}

func NewBasicAuthTaskProvider(credentialsProvider credentials.BasicAuthCredentialsProvider) BasicAuthTaskProvider {
	return BasicAuthTaskProvider{
		credentialsProvider: credentialsProvider,
	}
}

// GetNewTasks returns the list of new renovate tasks for the components. It uses such an algorithm:
// 1. Group components by namespace
// 2. Group components by platform
// 3. Group components by host
// 4. For each host creating tasksOnHost
// 5. For each component looking for an existing task with the same repository and adding a new branch to it
// 6. If there is no task with the same repository, looking for a task with the same credentials and adding a new repository to it
// 7. If there is no task with the same credentials, creating a new task and adding it to the tasksOnHost
// 8. Adding tasksOnHost to the newTasks
func (g BasicAuthTaskProvider) GetNewTasks(ctx context.Context, components []*git.ScmComponent) []*Task {
	log := ctrllog.FromContext(ctx)
	// Step 1
	componentNamespaceMap := git.NamespaceToComponentMap(components)
	log.V(logs.DebugLevel).Info("generating new renovate task in user's namespace for components", "count", len(components))
	var newTasks []*Task
	for namespace, componentsInNamespace := range componentNamespaceMap {
		log.V(logs.DebugLevel).Info("found components", "namespace", namespace, "count", len(componentsInNamespace))
		// Step 2
		platformToComponentMap := git.PlatformToComponentMap(componentsInNamespace)
		log.V(logs.DebugLevel).Info("found git platform on namespace", "namespace", namespace, "count", len(platformToComponentMap))
		for platform, componentsOnPlatform := range platformToComponentMap {
			log.V(logs.DebugLevel).Info("processing components on platform", "platform", platform, "count", len(componentsOnPlatform))
			// Step 3
			hostToComponentsMap := git.HostToComponentMap(componentsOnPlatform)
			log.V(logs.DebugLevel).Info("found hosts on platform", "namespace", namespace, "platform", platform, "count", len(hostToComponentsMap))
			// Step 4
			var tasksOnHost []*Task
			for host, componentsOnHost := range hostToComponentsMap {
				log.V(logs.DebugLevel).Info("processing components on host", "namespace", namespace, "platform", platform, "host", host, "count", len(componentsOnHost))
				for _, component := range componentsOnHost {
					// Step 5
					if !AddNewBranchToTheExistedRepositoryTasksOnTheSameHosts(tasksOnHost, component) {
						creds, err := g.credentialsProvider.GetBasicAuthCredentials(ctx, component)
						if err != nil {
							if !boerrors.IsBuildOpError(err, boerrors.EComponentGitSecretMissing) {
								log.Error(err, "failed to get basic auth credentials for component", "component", component)
							}
							continue
						}
						// Step 6
						if !AddNewRepoToTasksOnTheSameHostsWithSameCredentials(tasksOnHost, component, creds) {
							// Step 7
							tasksOnHost = append(tasksOnHost, NewBasicAuthTask(platform, host, creds, []*Repository{
								{
									Repository:   component.Repository(),
									BaseBranches: []string{component.Branch()},
								},
							}))
						}
					}
				}
			}
			//Step 8
			newTasks = append(newTasks, tasksOnHost...)
		}

	}
	log.V(logs.DebugLevel).Info("generated new renovate tasks", "count", len(newTasks))
	return newTasks
}

func NewBasicAuthTask(platform string, host string, credentials *credentials.BasicAuthCredentials, repositories []*Repository) *Task {
	return &Task{
		Platform:        platform,
		Username:        credentials.Username,
		GitAuthor:       fmt.Sprintf("%s <123456+%s[bot]@users.noreply.%s>", credentials.Username, credentials.Username, host),
		RenovatePattern: GetRenovatePatternConfiguration(),
		Token:           credentials.Password,
		Repositories:    repositories,
	}
}
