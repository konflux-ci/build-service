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
// 4. For each component get the basic auth credentials
// 5. For each component get branches and credentials to create Renovate task

func (g BasicAuthTaskProvider) GetNewTasks(ctx context.Context, components []*git.ScmComponent) []Task {
	log := ctrllog.FromContext(ctx)
	componentNamespaceMap := git.NamespaceToComponentMap(components)
	log.V(logs.DebugLevel).Info("generating new renovate task in user's namespace", "count", len(componentNamespaceMap))
	var newTasks []Task
	for namespaceName, componentsInNamespace := range componentNamespaceMap {
		log.V(logs.DebugLevel).Info("found component", "count", len(componentsInNamespace))

		platformToComponentMap := git.PlatformToComponentMap(componentsInNamespace)
		log.V(logs.DebugLevel).Info("found git platform", "count", len(platformToComponentMap))
		for platform, componentsOnPlatform := range platformToComponentMap {
			log.V(logs.DebugLevel).Info("processing components on platform", "platform", platform, "componentsOnPlatform", componentsOnPlatform)
			hostToComponentsMap := git.HostToComponentMap(componentsOnPlatform)
			log.V(logs.DebugLevel).Info("found host on platform", "count", len(hostToComponentsMap))
			for host, componentsOnHost := range hostToComponentsMap {
				log.V(logs.DebugLevel).Info("processing components on host", "host", host, "componentsOnHost", componentsOnHost)
				for _, component := range componentsOnHost {
					credentials, err := g.credentialsProvider.GetBasicAuthCredentials(ctx, component)
					if err != nil {
						if !boerrors.IsBuildOpError(err, boerrors.EComponentGitSecretMissing) {
							log.Error(err, "failed to get basic auth credentials for component", "component", component)
						}
						continue
					}
					componentRepoToBranchesMap := git.ComponentRepoToBranchesMap(componentsOnHost)
					log.V(logs.DebugLevel).Info("Found branches for components", "count", len(componentRepoToBranchesMap))
					var repositories []Repository
					for repo, branches := range componentRepoToBranchesMap {
						repositories = append(repositories, Repository{
							BaseBranches: branches,
							Repository:   repo,
						})
						log.V(logs.DebugLevel).Info("Creating new task for component", "repo", repo, "branches", branches, "namespace", namespaceName, "platform", platform)
						newTasks = append(newTasks, NewBasicAuthTask(platform, host, credentials, repositories))
					}
				}
			}
		}

	}

	return newTasks
}

func NewBasicAuthTask(platform string, host string, credentials *credentials.BasicAuthCredentials, repositories []Repository) Task {

	return Task{
		Platform:        platform,
		Username:        credentials.Username,
		GitAuthor:       fmt.Sprintf("%s <123456+%s[bot]@users.noreply.%s>", credentials.Username, credentials.Username, host),
		RenovatePattern: GetRenovatePatternConfiguration(),
		Token:           credentials.Password,
		Repositories:    repositories,
	}
}
