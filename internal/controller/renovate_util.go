package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/konflux-ci/application-api/api/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logger "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/konflux-ci/build-service/pkg/common"
	"github.com/konflux-ci/build-service/pkg/git"
	"github.com/konflux-ci/build-service/pkg/git/github"
	"github.com/konflux-ci/build-service/pkg/git/gitproviderfactory"
	"github.com/konflux-ci/build-service/pkg/k8s"
	"github.com/konflux-ci/build-service/pkg/logs"
	l "github.com/konflux-ci/build-service/pkg/logs"
)

const (
	RenovateImageEnvName               = "RENOVATE_IMAGE"
	DefaultRenovateImageUrl            = "quay.io/konflux-ci/mintmaker-renovate-image:083371d"
	DefaultRenovateUser                = "red-hat-konflux"
	CaConfigMapLabel                   = "config.openshift.io/inject-trusted-cabundle"
	CaConfigMapKey                     = "ca-bundle.crt"
	CaFilePath                         = "tls-ca-bundle.pem"
	CaMountPath                        = "/etc/pki/ca-trust/extracted/pem"
	CaVolumeMountName                  = "trusted-ca"
	BranchPrefix                       = "konflux/component-updates/"
	NamespaceWideRenovateConfigMapName = "namespace-wide-nudging-renovate-config"
	CustomRenovateConfigMapAnnotation  = "build.appstudio.openshift.io/nudge_renovate_config_map"
	DefaultRenovateLabel               = "konflux-nudge"

	RenovateConfigMapAutomergeKey             = "automerge"
	RenovateConfigMapCommitMessagePrefixKey   = "commitMessagePrefix"
	RenovateConfigMapCommitMessageSuffixKey   = "commitMessageSuffix"
	RenovateConfigMapFileMatchKey             = "fileMatch"
	RenovateConfigMapAutomergeTypeKey         = "automergeType"
	RenovateConfigMapPlatformAutomergeKey     = "platformAutomerge"
	RenovateConfigMapIgnoreTestsKey           = "ignoreTests"
	RenovateConfigMapGitLabIgnoreApprovalsKey = "gitLabIgnoreApprovals"
	RenovateConfigMapAutomergeScheduleKey     = "automergeSchedule"
	RenovateConfigMapLabelsKey                = "labels"
)

type renovateRepository struct {
	Repository   string   `json:"repository"`
	BaseBranches []string `json:"baseBranches,omitempty"`
}

type CustomRenovateOptions struct {
	Automerge             bool     `json:"automerge,omitempty"`
	AutomergeType         string   `json:"automergeType,omitempty"`
	PlatformAutomerge     bool     `json:"platformAutomerge,omitempty"`
	IgnoreTests           bool     `json:"ignoreTests,omitempty"`
	CommitMessagePrefix   string   `json:"commitMessagePrefix,omitempty"`
	CommitMessageSuffix   string   `json:"commitMessageSuffix,omitempty"`
	FileMatch             []string `json:"fileMatch,omitempty"`
	GitLabIgnoreApprovals bool     `json:"gitLabIgnoreApprovals,omitempty"`
	AutomergeSchedule     []string `json:"automergeSchedule,omitempty"`
	Labels                []string `json:"labels,omitempty"`
}

// UpdateTarget represents a target source code repository to be executed by Renovate with credentials and repositories
type updateTarget struct {
	ComponentName                  string
	ComponentCustomRenovateOptions *CustomRenovateOptions
	GitProvider                    string
	Username                       string
	GitAuthor                      string
	Token                          string
	Endpoint                       string
	Repositories                   []renovateRepository
	ImageRepositoryHost            string
	ImageRepositoryUsername        string
	ImageRepositoryPassword        string
}

type ComponentDependenciesUpdater struct {
	Client             client.Client
	Scheme             *runtime.Scheme
	EventRecorder      record.EventRecorder
	CredentialProvider *k8s.GitCredentialProvider
}

type CustomManager struct {
	FileMatch            []string `json:"fileMatch,omitempty"`
	CustomType           string   `json:"customType"`
	DatasourceTemplate   string   `json:"datasourceTemplate"`
	MatchStrings         []string `json:"matchStrings"`
	CurrentValueTemplate string   `json:"currentValueTemplate"`
	DepNameTemplate      string   `json:"depNameTemplate"`
}

type PackageRule struct {
	MatchPackagePatterns   []string `json:"matchPackagePatterns,omitempty"`
	MatchPackageNames      []string `json:"matchPackageNames,omitempty"`
	GroupName              string   `json:"groupName,omitempty"`
	BranchPrefix           string   `json:"branchPrefix,omitempty"`
	BranchTopic            string   `json:"branchTopic,omitempty"`
	AdditionalBranchPrefix string   `json:"additionalBranchPrefix,omitempty"`
	CommitMessageTopic     string   `json:"commitMessageTopic,omitempty"`
	CommitMessagePrefix    string   `json:"commitMessagePrefix,omitempty"`
	CommitMessageSuffix    string   `json:"commitMessageSuffix,omitempty"`
	CommitBody             string   `json:"commitBody,omitempty"`
	PRFooter               string   `json:"prFooter,omitempty"`
	PRHeader               string   `json:"prHeader,omitempty"`
	RecreateWhen           string   `json:"recreateWhen,omitempty"`
	RebaseWhen             string   `json:"rebaseWhen,omitempty"`
	Enabled                bool     `json:"enabled"`
}

type RenovateConfig struct {
	GitProvider           string               `json:"platform"`
	Username              string               `json:"username"`
	GitAuthor             string               `json:"gitAuthor"`
	Onboarding            bool                 `json:"onboarding"`
	RequireConfig         string               `json:"requireConfig"`
	Repositories          []renovateRepository `json:"repositories"`
	EnabledManagers       []string             `json:"enabledManagers"`
	Endpoint              string               `json:"endpoint"`
	CustomManagers        []CustomManager      `json:"customManagers,omitempty"`
	RegistryAliases       map[string]string    `json:"registryAliases,omitempty"`
	PackageRules          []PackageRule        `json:"packageRules,omitempty"`
	ForkProcessing        string               `json:"forkProcessing"`
	Extends               []string             `json:"extends"`
	DependencyDashboard   bool                 `json:"dependencyDashboard"`
	Labels                []string             `json:"labels"`
	Automerge             bool                 `json:"automerge,omitempty"`
	AutomergeType         string               `json:"automergeType,omitempty"`
	PlatformAutomerge     bool                 `json:"platformAutomerge,omitempty"`
	IgnoreTests           bool                 `json:"ignoreTests,omitempty"`
	GitLabIgnoreApprovals bool                 `json:"gitLabIgnoreApprovals,omitempty"`
	AutomergeSchedule     []string             `json:"automergeSchedule,omitempty"`
}

var DisableAllPackageRules = PackageRule{MatchPackagePatterns: []string{"*"}, Enabled: false}

var GenerateRenovateConfigForNudge func(target updateTarget, buildResult *BuildResult, gitRepoAtShaLink string, simpleBranchName bool) (RenovateConfig, error) = generateRenovateConfigForNudge

func NewComponentDependenciesUpdater(client client.Client, scheme *runtime.Scheme, eventRecorder record.EventRecorder) *ComponentDependenciesUpdater {
	return &ComponentDependenciesUpdater{Client: client, Scheme: scheme, EventRecorder: eventRecorder, CredentialProvider: k8s.NewGitCredentialProvider(client)}
}

// GetUpdateTargetsBasicAuth This method returns targets for components based on basic auth
func (u ComponentDependenciesUpdater) GetUpdateTargetsBasicAuth(ctx context.Context, componentList []v1alpha1.Component, imageRepositoryHost, imageRepositoryUsername, imageRepositoryPassword string) []updateTarget {
	log := logger.FromContext(ctx)
	targetsToUpdate := []updateTarget{}

	for _, comp := range componentList {
		component := comp
		gitProvider, err := getGitProvider(component)
		if err != nil {
			log.Error(err, "error detecting git provider", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			continue
		}

		repoUrl := getGitRepoUrl(component)
		scmComponent, err := git.NewScmComponent(gitProvider, repoUrl, component.Spec.Source.GitSource.Revision, component.Name, component.Namespace)
		if err != nil {
			log.Error(err, "error parsing component", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			continue
		}

		creds, err := u.CredentialProvider.GetBasicAuthCredentials(ctx, scmComponent)
		if err != nil {
			log.Error(err, "error getting basic auth credentials for component", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			log.Info(fmt.Sprintf("for repository %s", repoUrl))
			continue
		}

		branch := scmComponent.Branch()
		if branch == git.InternalDefaultBranch {
			pacConfig := map[string][]byte{"password": []byte(creds.Password)}
			if creds.Username != "" {
				pacConfig["username"] = []byte(creds.Username)
			}

			gitClient, err := gitproviderfactory.CreateGitClient(gitproviderfactory.GitClientConfig{
				PacSecretData:             pacConfig,
				GitProvider:               gitProvider,
				RepoUrl:                   repoUrl,
				IsAppInstallationExpected: true,
			})
			if err != nil {
				log.Error(err, "error create git client for component", "ComponentName", component.Name, "RepoUrl", repoUrl)
				continue
			}
			defaultBranch, err := gitClient.GetDefaultBranchWithChecks(repoUrl)
			if err != nil {
				log.Error(err, "error get git default branch for component", "ComponentName", component.Name, "RepoUrl", repoUrl)
				continue
			}
			branch = defaultBranch
		}
		repositories := []renovateRepository{}
		repositories = append(repositories, renovateRepository{
			Repository:   scmComponent.Repository(),
			BaseBranches: []string{branch},
		})

		username := creds.Username
		if username == "" {
			username = DefaultRenovateUser
		}

		customRenovateOptions, err := u.ReadCustomRenovateConfigMap(ctx, &component)
		if err != nil {
			// even if it fails on reading custom config Map, we should still perform nudging as we have the core renovate config
			log.Error(err, "failed to read custom renovate config map, will still continue with nudging", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
		}

		targetsToUpdate = append(targetsToUpdate, updateTarget{
			ComponentName:                  component.Name,
			ComponentCustomRenovateOptions: customRenovateOptions,
			GitProvider:                    gitProvider,
			Username:                       username,
			GitAuthor:                      fmt.Sprintf("%s <123456+%s[bot]@users.noreply.%s>", username, username, scmComponent.RepositoryHost()),
			Token:                          creds.Password,
			Endpoint:                       git.BuildAPIEndpoint(gitProvider).APIEndpoint(scmComponent.RepositoryHost()),
			Repositories:                   repositories,
			ImageRepositoryHost:            imageRepositoryHost,
			ImageRepositoryUsername:        imageRepositoryUsername,
			ImageRepositoryPassword:        imageRepositoryPassword,
		})
		log.Info("component to update for basic auth", "component", component.Name, "repositories", repositories)
	}

	return targetsToUpdate
}

// GetUpdateTargetsGithubApp This method returns targets for components based on github app
func (u ComponentDependenciesUpdater) GetUpdateTargetsGithubApp(ctx context.Context, componentList []v1alpha1.Component, imageRepositoryHost, imageRepositoryUsername, imageRepositoryPassword string) []updateTarget {
	log := logger.FromContext(ctx)
	// Check if GitHub Application is used, if not then skip
	pacSecret := corev1.Secret{}
	globalPaCSecretKey := types.NamespacedName{Namespace: BuildServiceNamespaceName, Name: PipelinesAsCodeGitHubAppSecretName}
	if err := u.Client.Get(ctx, globalPaCSecretKey, &pacSecret); err != nil {
		u.EventRecorder.Event(&pacSecret, "Warning", "ErrorReadingPaCSecret", err.Error())
		if errors.IsNotFound(err) {
			log.Error(err, "not found Pipelines as Code secret in %s namespace: %w", globalPaCSecretKey.Namespace, err, logs.Action, logs.ActionView)
		} else {
			log.Error(err, "failed to get Pipelines as Code secret in %s namespace: %w", globalPaCSecretKey.Namespace, err, logs.Action, logs.ActionView)
		}
		return nil
	}
	isApp := IsPaCApplicationConfigured("github", pacSecret.Data)
	if !isApp {
		log.Info("GitHub App is not set")
		return nil
	}

	// Load GitHub App and get GitHub Installations
	githubAppIdStr := string(pacSecret.Data[PipelinesAsCodeGithubAppIdKey])
	privateKey := pacSecret.Data[PipelinesAsCodeGithubPrivateKey]

	// Match installed repositories with Components and get custom branch if defined
	targetsToUpdate := []updateTarget{}
	var slug string
	var appBotName string
	var appBotId int64
	for _, comp := range componentList {
		component := comp
		if component.Spec.Source.GitSource == nil {
			continue
		}

		gitProvider, err := getGitProvider(component)
		if err != nil {
			log.Error(err, "error detecting git provider", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			continue
		}
		if gitProvider != "github" {
			continue
		}

		gitSource := component.Spec.Source.GitSource

		url := getGitRepoUrl(component)
		log.Info("getting app installation for component repository", "ComponentName", component.Name, "ComponentNamespace", component.Namespace, "RepositoryUrl", url)
		githubAppInstallation, slugTmp, err := github.GetAppInstallationsForRepository(githubAppIdStr, privateKey, url)
		if slug == "" {
			slug = slugTmp
		}
		if err != nil {
			log.Error(err, "Failed to get GitHub app installation for component", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			continue
		}

		if appBotId == 0 {
			pacConfig := map[string][]byte{PipelinesAsCodeGithubPrivateKey: []byte(privateKey), PipelinesAsCodeGithubAppIdKey: []byte(githubAppIdStr)}
			gitClient, err := gitproviderfactory.CreateGitClient(gitproviderfactory.GitClientConfig{
				PacSecretData:             pacConfig,
				GitProvider:               gitProvider,
				RepoUrl:                   url,
				IsAppInstallationExpected: true,
			})
			if err != nil {
				log.Error(err, "error create git client for component", "ComponentName", component.Name, "RepoUrl", component.Spec.Source.GitSource.URL)
				continue
			}
			appBotName = fmt.Sprintf("%s[bot]", slug)
			botID, err := gitClient.GetAppUserId(appBotName)
			if err != nil {
				log.Error(err, "Failed to get application user info", "userName", appBotName)
				continue
			}
			appBotId = botID
		}

		branch := gitSource.Revision
		if branch == "" {
			branch = git.InternalDefaultBranch
		}

		repositories := []renovateRepository{}
		for _, repository := range githubAppInstallation.Repositories {
			if branch == git.InternalDefaultBranch {
				branch = repository.GetDefaultBranch()
			}

			repositories = append(repositories, renovateRepository{
				BaseBranches: []string{branch},
				Repository:   repository.GetFullName(),
			})
		}
		// Do not add target which has no matching repositories
		if len(repositories) == 0 {
			log.Info("no repositories found in the installation", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			continue
		}

		customRenovateOptions, err := u.ReadCustomRenovateConfigMap(ctx, &component)
		if err != nil {
			// even if it fails on reading custom config Map, we should still perform nudging as we have the core renovate config
			log.Error(err, "failed to read custom renovate config map, will still continue with nudging", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
		}

		// hardcoding the number in gitAuthor because mintmaker has it hardcoded as well, so that way mintmaker will recognize the same author
		targetsToUpdate = append(targetsToUpdate, updateTarget{
			ComponentName:                  component.Name,
			ComponentCustomRenovateOptions: customRenovateOptions,
			GitProvider:                    gitProvider,
			Username:                       appBotName,
			GitAuthor:                      fmt.Sprintf("%s <%d+%s@users.noreply.github.com>", slug, appBotId, appBotName),
			Token:                          githubAppInstallation.Token,
			Endpoint:                       git.BuildAPIEndpoint("github").APIEndpoint("github.com"),
			Repositories:                   repositories,
			ImageRepositoryHost:            imageRepositoryHost,
			ImageRepositoryUsername:        imageRepositoryUsername,
			ImageRepositoryPassword:        imageRepositoryPassword,
		})
		log.Info("component to update for installations", "component", component.Name, "repositories", repositories)
	}

	return targetsToUpdate
}

// ReadCustomRenovateConfigMaps returns custom renovate options from config map which is either defined in
// component's annotation CustomRenovateConfigMapAnnotation (which takes precedence),
// or from namespace wide config NamespaceWideRenovateConfigMapName
func (u ComponentDependenciesUpdater) ReadCustomRenovateConfigMap(ctx context.Context, component *v1alpha1.Component) (*CustomRenovateOptions, error) {
	log := logger.FromContext(ctx)
	customRenovateOptions := CustomRenovateOptions{}

	customRenovateConfigMapName := NamespaceWideRenovateConfigMapName
	customComponentRenovateConfigMapName := readComponentCustomRenovateConfigMapAnnotation(component)
	if customComponentRenovateConfigMapName != "" {
		customRenovateConfigMapName = customComponentRenovateConfigMapName
	}

	customRenovateConfigMap := &corev1.ConfigMap{}
	if err := u.Client.Get(ctx, types.NamespacedName{Name: customRenovateConfigMapName, Namespace: component.Namespace}, customRenovateConfigMap); err != nil {
		if errors.IsNotFound(err) {
			if customRenovateConfigMapName != NamespaceWideRenovateConfigMapName {
				log.Info("custom renovate config map in component annotation doesn't exist", "configMapName", customRenovateConfigMapName)
			}
			// no need to fail if custom renovate config map doesn't exist
			return &customRenovateOptions, nil
		}
		return &customRenovateOptions, err
	}
	log.Info("will use custom renovate config map", "configMapName", customRenovateConfigMapName)

	automergeOption, automergeExists := customRenovateConfigMap.Data[RenovateConfigMapAutomergeKey]
	if automergeExists {
		automergeValue, err := strconv.ParseBool(automergeOption)
		if err != nil {
			log.Error(err, "can't parse automerge option in configmap", "configMapName", customRenovateConfigMapName, "automergeValue", automergeOption)
		} else {
			customRenovateOptions.Automerge = automergeValue
		}
	}

	platformAutomergeOption, platformAutomergeExists := customRenovateConfigMap.Data[RenovateConfigMapPlatformAutomergeKey]
	if platformAutomergeExists {
		platformAutomergeValue, err := strconv.ParseBool(platformAutomergeOption)
		if err != nil {
			log.Error(err, "can't parse platformAutomerge option in configmap", "configMapName", customRenovateConfigMapName, "platformAutomergeValue", platformAutomergeOption)
		} else {
			customRenovateOptions.PlatformAutomerge = platformAutomergeValue
		}
	}

	ignoreTestsOption, ignoreTestsExists := customRenovateConfigMap.Data[RenovateConfigMapIgnoreTestsKey]
	if ignoreTestsExists {
		ignoreTestsValue, err := strconv.ParseBool(ignoreTestsOption)
		if err != nil {
			log.Error(err, "can't parse ignoreTests option in configmap", "configMapName", customRenovateConfigMapName, "ignoreTestsValue", ignoreTestsOption)
		} else {
			customRenovateOptions.IgnoreTests = ignoreTestsValue
		}
	}

	gitLabIgnoreApprovalsOption, gitLabIgnoreApprovalsExists := customRenovateConfigMap.Data[RenovateConfigMapGitLabIgnoreApprovalsKey]
	if gitLabIgnoreApprovalsExists {
		gitLabIgnoreApprovalsValue, err := strconv.ParseBool(gitLabIgnoreApprovalsOption)
		if err != nil {
			log.Error(err, "can't parse gitLabIgnoreApprovals option in configmap", "configMapName", customRenovateConfigMapName,
				"gitLabIgnoreApprovalsValue", gitLabIgnoreApprovalsOption)
		} else {
			customRenovateOptions.GitLabIgnoreApprovals = gitLabIgnoreApprovalsValue
		}
	}

	automergeTypeOption, automergeTypeExists := customRenovateConfigMap.Data[RenovateConfigMapAutomergeTypeKey]
	if automergeTypeExists {
		customRenovateOptions.AutomergeType = automergeTypeOption
	}

	commitMessagePrefixOption, commitMessagePrefixExists := customRenovateConfigMap.Data[RenovateConfigMapCommitMessagePrefixKey]
	if commitMessagePrefixExists {
		customRenovateOptions.CommitMessagePrefix = commitMessagePrefixOption
	}

	commitMessageSuffixOption, commitMessageSuffixExists := customRenovateConfigMap.Data[RenovateConfigMapCommitMessageSuffixKey]
	if commitMessageSuffixExists {
		customRenovateOptions.CommitMessageSuffix = commitMessageSuffixOption
	}

	fileMatchOption, fileMatchExists := customRenovateConfigMap.Data[RenovateConfigMapFileMatchKey]
	if fileMatchExists {
		fileMatchParts := strings.Split(fileMatchOption, ",")
		for i := range fileMatchParts {
			fileMatchParts[i] = strings.TrimSpace(fileMatchParts[i])
		}
		customRenovateOptions.FileMatch = fileMatchParts
	}

	automergeScheduleOption, automergeScheduleExists := customRenovateConfigMap.Data[RenovateConfigMapAutomergeScheduleKey]
	if automergeScheduleExists {
		automergeScheduleParts := strings.Split(automergeScheduleOption, ";")
		for i := range automergeScheduleParts {
			automergeScheduleParts[i] = strings.TrimSpace(automergeScheduleParts[i])
		}
		customRenovateOptions.AutomergeSchedule = automergeScheduleParts
	}

	labelsOption, labelsExists := customRenovateConfigMap.Data[RenovateConfigMapLabelsKey]
	if labelsExists {
		labelsParts := strings.Split(labelsOption, ",")
		for i := range labelsParts {
			labelsParts[i] = strings.TrimSpace(labelsParts[i])
		}
		customRenovateOptions.Labels = labelsParts
	}

	return &customRenovateOptions, nil
}

// readComponentCustomRenovateConfigMapAnnotation returns name of ConfigMap with custom renovate settings for nudge.
// The ConfigMap name is taken from CustomRenovateConfigMapAnnotation annotation on the Component.
func readComponentCustomRenovateConfigMapAnnotation(component *v1alpha1.Component) string {
	if component.Annotations == nil {
		return ""
	}

	customRenovateConfigMapAnnotation, customRenovateConfigMapAnnotationExists := component.Annotations[CustomRenovateConfigMapAnnotation]
	if customRenovateConfigMapAnnotationExists && customRenovateConfigMapAnnotation != "" {
		return customRenovateConfigMapAnnotation
	}
	return ""
}

// generateRenovateConfigForNudge returns renovate config for specific component in the target
func generateRenovateConfigForNudge(target updateTarget, buildResult *BuildResult, gitRepoAtShaLink string, simpleBranchName bool) (RenovateConfig, error) {
	fileMatchParts := strings.Split(buildResult.FileMatches, ",")
	for i := range fileMatchParts {
		fileMatchParts[i] = strings.TrimSpace(fileMatchParts[i])
	}

	var matchStrings []string
	var registryAliases = make(map[string]string)
	var customManagers []CustomManager
	var packageRules []PackageRule
	var matchPackageNames []string
	matchStrings = append(matchStrings, buildResult.BuiltImageRepository+"(:.*)?@(?<currentDigest>sha256:[a-f0-9]+)")
	matchPackageNames = append(matchPackageNames, buildResult.BuiltImageRepository)

	for _, drepository := range buildResult.DistributionRepositories {
		matchStrings = append(matchStrings, drepository+"(:.*)?@(?<currentDigest>sha256:[a-f0-9]+)")
		matchPackageNames = append(matchPackageNames, drepository)
		registryAliases[drepository] = buildResult.BuiltImageRepository

	}

	// use Filematch from custom renovate confing rather than from pipeline run annotation
	if target.ComponentCustomRenovateOptions.FileMatch != nil {
		fileMatchParts = target.ComponentCustomRenovateOptions.FileMatch

	}

	// add default label
	labels := []string{DefaultRenovateLabel}
	if target.ComponentCustomRenovateOptions.Labels != nil {
		labels = append(labels, target.ComponentCustomRenovateOptions.Labels...)
	}

	customManagers = append(customManagers, CustomManager{
		FileMatch:            fileMatchParts,
		CustomType:           "regex",
		DatasourceTemplate:   "docker",
		MatchStrings:         matchStrings,
		CurrentValueTemplate: buildResult.BuiltImageTag,
		DepNameTemplate:      buildResult.BuiltImageRepository,
	})

	nudgingBranchName := fmt.Sprintf("%s-", target.ComponentName)
	if simpleBranchName {
		nudgingBranchName = ""
	}
	packageRules = append(packageRules, DisableAllPackageRules)
	packageRules = append(packageRules, PackageRule{
		MatchPackageNames:      matchPackageNames,
		GroupName:              fmt.Sprintf("Component Update %s", buildResult.Component.Name),
		BranchPrefix:           BranchPrefix,
		BranchTopic:            buildResult.Component.Name,
		AdditionalBranchPrefix: nudgingBranchName,
		CommitMessageTopic:     buildResult.Component.Name,
		PRFooter:               "To execute skipped test pipelines write comment `/ok-to-test`",
		PRHeader:               fmt.Sprintf("Image created from '%s'", gitRepoAtShaLink),
		RecreateWhen:           "always",
		RebaseWhen:             "behind-base-branch",
		Enabled:                true,
		CommitBody:             fmt.Sprintf("Image created from '%s'\n\nSigned-off-by: %s", gitRepoAtShaLink, target.GitAuthor),
		CommitMessagePrefix:    target.ComponentCustomRenovateOptions.CommitMessagePrefix,
		CommitMessageSuffix:    target.ComponentCustomRenovateOptions.CommitMessageSuffix,
	})

	renovateConfig := RenovateConfig{
		GitProvider:   target.GitProvider,
		Username:      target.Username,
		GitAuthor:     target.GitAuthor,
		Onboarding:    false,
		RequireConfig: "ignored",
		Repositories:  target.Repositories,
		// was 'regex' before but because: https://docs.renovatebot.com/configuration-options/#enabledmanagers
		EnabledManagers:       []string{"custom.regex"},
		Endpoint:              target.Endpoint,
		CustomManagers:        customManagers,
		RegistryAliases:       registryAliases,
		PackageRules:          packageRules,
		ForkProcessing:        "enabled",
		Extends:               []string{},
		DependencyDashboard:   false,
		Labels:                labels,
		Automerge:             target.ComponentCustomRenovateOptions.Automerge,
		PlatformAutomerge:     target.ComponentCustomRenovateOptions.PlatformAutomerge,
		IgnoreTests:           target.ComponentCustomRenovateOptions.IgnoreTests,
		AutomergeType:         target.ComponentCustomRenovateOptions.AutomergeType,
		GitLabIgnoreApprovals: target.ComponentCustomRenovateOptions.GitLabIgnoreApprovals,
		AutomergeSchedule:     target.ComponentCustomRenovateOptions.AutomergeSchedule,
	}

	return renovateConfig, nil
}

// CreateRenovaterPipeline will create a renovate pipeline in the user namespace, to update component dependencies.
// The reasons for using a pipeline in the component namespace instead of a Job in the system namespace is as follows:
// - The user namespace has direct access to secrets to allow updating private images
// - Job's are removed after a timeout, so lots of nudges in a short period could make the namespace unusable due to pod Quota, while pipelines are pruned much more aggressively
// - Users can view the results of pipelines and the results are stored, making debugging much easier
// - Tekton automatically provides docker config from linked service accounts for private images, with a job I would need to implement this manually
//
// Warning: the installation token used here should only be scoped to the individual repositories being updated
func (u ComponentDependenciesUpdater) CreateRenovaterPipeline(ctx context.Context, namespace string, targets []updateTarget, debug, simpleBranchName bool, buildResult *BuildResult, gitRepoAtShaLink string) error {
	log := logger.FromContext(ctx)
	log.Info(fmt.Sprintf("Creating renovate pipeline for %d components", len(targets)))

	if len(targets) == 0 {
		return nil
	}
	timestamp := time.Now().Unix()
	nameSuffix := fmt.Sprintf("%d-%s", timestamp, RandomString(5))
	name := fmt.Sprintf("renovate-pipeline-%s", nameSuffix)
	caConfigMapName := fmt.Sprintf("renovate-ca-%s", nameSuffix)
	secretTokens := map[string]string{}
	configmaps := map[string]string{}
	renovateCmds := []string{}

	for _, target := range targets {
		randomStr1 := RandomString(5)
		randomStr2 := RandomString(10)
		randomStr3 := RandomString(10)
		secretTokens[randomStr2] = target.Token
		secretTokens[randomStr3] = target.ImageRepositoryPassword
		configString := ""
		configType := "json"

		renovateConfig, err := GenerateRenovateConfigForNudge(target, buildResult, gitRepoAtShaLink, simpleBranchName)
		if err != nil {
			return err
		}

		config, err := json.Marshal(renovateConfig)
		if err != nil {
			return err
		}
		configString = string(config)

		log.Info(fmt.Sprintf("Creating renovate config map entry for %s component with length %d and value %s", target.ComponentName, len(configString), configString))

		configmaps[fmt.Sprintf("%s-%s.%s", target.ComponentName, randomStr1, configType)] = configString
		hostRules := fmt.Sprintf("\"[{'matchHost':'%s','username':'%s','password':'${TOKEN_%s}'}]\"", target.ImageRepositoryHost, target.ImageRepositoryUsername, randomStr3)

		// we are passing host rules via variable, because we can't resolve variable in json config
		// also this way we can use custom provided config without any modifications
		renovateCmds = append(renovateCmds,
			fmt.Sprintf("RENOVATE_X_GITLAB_MERGE_REQUEST_DELAY=5000 RENOVATE_X_GITLAB_AUTO_MERGEABLE_CHECK_ATTEMPS=11 RENOVATE_PR_HOURLY_LIMIT=0 RENOVATE_PR_CONCURRENT_LIMIT=0 RENOVATE_TOKEN=$TOKEN_%s RENOVATE_CONFIG_FILE=/configs/%s-%s.%s RENOVATE_HOST_RULES=%s renovate", randomStr2, target.ComponentName, randomStr1, configType, hostRules),
		)
	}
	if len(renovateCmds) == 0 {
		return nil
	}

	allCaConfigMaps := &corev1.ConfigMapList{}
	opts := client.ListOption(&client.MatchingLabels{
		CaConfigMapLabel: "true",
	})

	if err := u.Client.List(ctx, allCaConfigMaps, client.InNamespace(BuildServiceNamespaceName), opts); err != nil {
		return fmt.Errorf("failed to list config maps with label %s in %s namespace: %w", CaConfigMapLabel, BuildServiceNamespaceName, err)
	}
	caConfigData := ""
	if len(allCaConfigMaps.Items) > 0 {
		log.Info("will use CA config map", "name", allCaConfigMaps.Items[0].ObjectMeta.Name)
		caConfigData = allCaConfigMaps.Items[0].Data[CaConfigMapKey]
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		StringData: secretTokens,
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: configmaps,
	}

	var caConfigMap *corev1.ConfigMap
	if caConfigData != "" {
		configMapData := map[string]string{CaConfigMapKey: caConfigData}
		caConfigMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      caConfigMapName,
				Namespace: namespace,
			},
			Data: configMapData,
		}
	}

	renovatePipelineServiceAccountName := getBuildPipelineServiceAccountName(buildResult.Component)
	if err := u.Client.Get(ctx, types.NamespacedName{Name: renovatePipelineServiceAccountName, Namespace: namespace}, &corev1.ServiceAccount{}); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("Failed to read service account %s in namespace %s", renovatePipelineServiceAccountName, namespace), l.Action, l.ActionView)
			return err
		}
		// Fall back to deprecated appstudio-pipline Service Account
		renovatePipelineServiceAccountName = buildPipelineServiceAccountName
	}

	trueBool := true
	falseBool := false
	renovateImageUrl := os.Getenv(RenovateImageEnvName)
	if renovateImageUrl == "" {
		renovateImageUrl = DefaultRenovateImageUrl
	}
	pipelineRun := &tektonapi.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: tektonapi.PipelineRunSpec{
			PipelineSpec: &tektonapi.PipelineSpec{
				Tasks: []tektonapi.PipelineTask{{
					Name: "renovate",
					TaskSpec: &tektonapi.EmbeddedTask{
						TaskSpec: tektonapi.TaskSpec{
							Steps: []tektonapi.Step{{
								Name:  "renovate",
								Image: renovateImageUrl,
								EnvFrom: []corev1.EnvFromSource{
									{
										Prefix: "TOKEN_",
										SecretRef: &corev1.SecretEnvSource{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: name,
											},
										},
									},
								},
								Command: []string{"bash", "-c", strings.Join(renovateCmds, "; ")},
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      name,
										MountPath: "/configs",
									},
								},
								SecurityContext: &corev1.SecurityContext{
									Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
									RunAsNonRoot:             &trueBool,
									AllowPrivilegeEscalation: &falseBool,
									SeccompProfile: &corev1.SeccompProfile{
										Type: corev1.SeccompProfileTypeRuntimeDefault,
									},
								},
							}},
							Volumes: []corev1.Volume{
								{
									Name: name,
									VolumeSource: corev1.VolumeSource{
										ConfigMap: &corev1.ConfigMapVolumeSource{
											LocalObjectReference: corev1.LocalObjectReference{Name: name},
										},
									},
								},
							},
						},
					},
				}},
			},
			TaskRunTemplate: tektonapi.PipelineTaskRunTemplate{
				ServiceAccountName: renovatePipelineServiceAccountName,
			},
		},
	}

	if caConfigData != "" {
		caVolume := corev1.Volume{
			Name: CaVolumeMountName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: caConfigMapName},
					Items: []corev1.KeyToPath{
						{
							Key:  CaConfigMapKey,
							Path: CaFilePath,
						},
					},
				},
			},
		}
		caVolumeMount := corev1.VolumeMount{
			Name:      CaVolumeMountName,
			MountPath: CaMountPath,
			ReadOnly:  true,
		}
		pipelineRun.Spec.PipelineSpec.Tasks[0].TaskSpec.TaskSpec.Volumes = append(pipelineRun.Spec.PipelineSpec.Tasks[0].TaskSpec.TaskSpec.Volumes, caVolume)
		pipelineRun.Spec.PipelineSpec.Tasks[0].TaskSpec.TaskSpec.Steps[0].VolumeMounts = append(pipelineRun.Spec.PipelineSpec.Tasks[0].TaskSpec.TaskSpec.Steps[0].VolumeMounts, caVolumeMount)
	}

	if debug {
		pipelineRun.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Env = append(pipelineRun.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Env, corev1.EnvVar{Name: "LOG_LEVEL", Value: "debug"})
	}

	if err := u.Client.Create(ctx, pipelineRun); err != nil {
		return err
	}
	// We create the PipelineRun first, and it will wait for the secret and configmap to be created
	if err := controllerutil.SetOwnerReference(pipelineRun, configMap, u.Scheme); err != nil {
		return err
	}
	if err := controllerutil.SetOwnerReference(pipelineRun, secret, u.Scheme); err != nil {
		return err
	}

	if caConfigData != "" {
		if err := controllerutil.SetOwnerReference(pipelineRun, caConfigMap, u.Scheme); err != nil {
			return err
		}
		if err := u.Client.Create(ctx, caConfigMap); err != nil {
			return err
		}
	}

	if err := u.Client.Create(ctx, secret); err != nil {
		return err
	}
	if err := u.Client.Create(ctx, configMap); err != nil {
		return err
	}
	log.Info(fmt.Sprintf("Renovate pipeline %s triggered", pipelineRun.Name), logs.Action, logs.ActionAdd)

	return nil
}
