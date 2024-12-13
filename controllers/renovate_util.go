package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
)

const (
	RenovateImageEnvName              = "RENOVATE_IMAGE"
	DefaultRenovateImageUrl           = "quay.io/konflux-ci/mintmaker-renovate-image:cdbc220"
	DefaultRenovateUser               = "red-hat-konflux"
	CaConfigMapLabel                  = "config.openshift.io/inject-trusted-cabundle"
	CaConfigMapKey                    = "ca-bundle.crt"
	CaFilePath                        = "tls-ca-bundle.pem"
	CaMountPath                       = "/etc/pki/ca-trust/extracted/pem"
	CaVolumeMountName                 = "trusted-ca"
	NamespaceWideRenovateConfigName   = "namespace-wide-nudging-renovate-config"
	ComponentRenovateConfigNamePrefix = "nudging-renovate-config-"
	ConfigKeyJson                     = "config.json"
	ConfigKeyJs                       = "config.js"
)

type renovateRepository struct {
	Repository   string   `json:"repository"`
	BaseBranches []string `json:"baseBranches,omitempty"`
}

// UpdateTarget represents a target source code repository to be executed by Renovate with credentials and repositories
type updateTarget struct {
	ComponentName           string
	GitProvider             string
	Username                string
	GitAuthor               string
	Token                   string
	Endpoint                string
	Repositories            []renovateRepository
	ImageRepositoryHost     string
	ImageRepositoryUsername string
	ImageRepositoryPassword string
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
	MatchPackagePatterns []string `json:"matchPackagePatterns,omitempty"`
	MatchPackageNames    []string `json:"matchPackageNames,omitempty"`
	GroupName            string   `json:"groupName,omitempty"`
	BranchName           string   `json:"branchName,omitempty"`
	CommitMessageTopic   string   `json:"commitMessageTopic,omitempty"`
	PRFooter             string   `json:"prFooter,omitempty"`
	RecreateWhen         string   `json:"recreateWhen,omitempty"`
	RebaseWhen           string   `json:"rebaseWhen,omitempty"`
	Enabled              bool     `json:"enabled"`
	FollowTag            string   `json:"followTag,omitempty"`
}

type RenovateConfig struct {
	GitProvider         string               `json:"platform"`
	Username            string               `json:"username"`
	GitAuthor           string               `json:"gitAuthor"`
	Onboarding          bool                 `json:"onboarding"`
	RequireConfig       string               `json:"requireConfig"`
	Repositories        []renovateRepository `json:"repositories"`
	EnabledManagers     []string             `json:"enabledManagers"`
	Endpoint            string               `json:"endpoint"`
	CustomManagers      []CustomManager      `json:"customManagers,omitempty"`
	RegistryAliases     map[string]string    `json:"registryAliases,omitempty"`
	PackageRules        []PackageRule        `json:"packageRules,omitempty"`
	ForkProcessing      string               `json:"forkProcessing"`
	Extends             []string             `json:"extends"`
	DependencyDashboard bool                 `json:"dependencyDashboard"`
	Labels              []string             `json:"labels"`
}

var DisableAllPackageRules = PackageRule{MatchPackagePatterns: []string{"*"}, Enabled: false}

var GenerateRenovateConfigForNudge func(target updateTarget, buildResult *BuildResult) (RenovateConfig, error) = generateRenovateConfigForNudge

func NewComponentDependenciesUpdater(client client.Client, scheme *runtime.Scheme, eventRecorder record.EventRecorder) *ComponentDependenciesUpdater {
	return &ComponentDependenciesUpdater{Client: client, Scheme: scheme, EventRecorder: eventRecorder, CredentialProvider: k8s.NewGitCredentialProvider(client)}
}

// GetUpdateTargetsBasicAuth This method returns targets for components based on basic auth
func (u ComponentDependenciesUpdater) GetUpdateTargetsBasicAuth(ctx context.Context, componentList []v1alpha1.Component, imageRepositoryHost, imageRepositoryUsername, imageRepositoryPassword string) []updateTarget {
	log := logger.FromContext(ctx)
	targetsToUpdate := []updateTarget{}

	for _, component := range componentList {
		gitProvider, err := getGitProvider(component)
		if err != nil {
			log.Error(err, "error detecting git provider", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			continue
		}

		scmComponent, err := git.NewScmComponent(gitProvider, component.Spec.Source.GitSource.URL, component.Spec.Source.GitSource.Revision, component.Name, component.Namespace)
		if err != nil {
			log.Error(err, "error parsing component", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			continue
		}

		creds, err := u.CredentialProvider.GetBasicAuthCredentials(ctx, scmComponent)
		if err != nil {
			log.Error(err, "error getting basic auth credentials for component", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			log.Info(fmt.Sprintf("for repository %s", component.Spec.Source.GitSource.URL))
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
				RepoUrl:                   component.Spec.Source.GitSource.URL,
				IsAppInstallationExpected: true,
			})
			if err != nil {
				log.Error(err, "error create git client for component", "ComponentName", component.Name, "RepoUrl", component.Spec.Source.GitSource.URL)
				continue
			}
			defaultBranch, err := gitClient.GetDefaultBranch(component.Spec.Source.GitSource.URL)
			if err != nil {
				log.Error(err, "error get git default branch for component", "ComponentName", component.Name, "RepoUrl", component.Spec.Source.GitSource.URL)
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

		targetsToUpdate = append(targetsToUpdate, updateTarget{
			ComponentName:           component.Name,
			GitProvider:             gitProvider,
			Username:                username,
			GitAuthor:               fmt.Sprintf("%s <123456+%s[bot]@users.noreply.%s>", username, username, scmComponent.RepositoryHost()),
			Token:                   creds.Password,
			Endpoint:                git.BuildAPIEndpoint(gitProvider).APIEndpoint(scmComponent.RepositoryHost()),
			Repositories:            repositories,
			ImageRepositoryHost:     imageRepositoryHost,
			ImageRepositoryUsername: imageRepositoryUsername,
			ImageRepositoryPassword: imageRepositoryPassword,
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
	for _, component := range componentList {
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

		url := strings.TrimSuffix(strings.TrimSuffix(gitSource.URL, ".git"), "/")
		log.Info("getting app installation for component repository", "ComponentName", component.Name, "ComponentNamespace", component.Namespace, "RepositoryUrl", url)
		githubAppInstallation, slugTmp, err := github.GetAppInstallationsForRepository(githubAppIdStr, privateKey, url)
		if slug == "" {
			slug = slugTmp
		}
		if err != nil {
			log.Error(err, "Failed to get GitHub app installation for component", "ComponentName", component.Name, "ComponentNamespace", component.Namespace)
			continue
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

		targetsToUpdate = append(targetsToUpdate, updateTarget{
			ComponentName:           component.Name,
			GitProvider:             gitProvider,
			Username:                fmt.Sprintf("%s[bot]", slug),
			// hardcoding the number because mintmaker has it hardcoded as well, so that way mintmaker will recognize the same author
			GitAuthor:               fmt.Sprintf("%s <126015336+%s[bot]@users.noreply.github.com>", slug, slug),
			Token:                   githubAppInstallation.Token,
			Endpoint:                git.BuildAPIEndpoint("github").APIEndpoint("github.com"),
			Repositories:            repositories,
			ImageRepositoryHost:     imageRepositoryHost,
			ImageRepositoryUsername: imageRepositoryUsername,
			ImageRepositoryPassword: imageRepositoryPassword,
		})
		log.Info("component to update for installations", "component", component.Name, "repositories", repositories)
	}

	return targetsToUpdate
}

// generateRenovateConfigForNudge This method returns renovate config for target
func generateRenovateConfigForNudge(target updateTarget, buildResult *BuildResult) (RenovateConfig, error) {
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

	customManagers = append(customManagers, CustomManager{
		FileMatch:            fileMatchParts,
		CustomType:           "regex",
		DatasourceTemplate:   "docker",
		MatchStrings:         matchStrings,
		CurrentValueTemplate: buildResult.BuiltImageTag,
		DepNameTemplate:      buildResult.BuiltImageRepository,
	})

	packageRules = append(packageRules, DisableAllPackageRules)
	packageRules = append(packageRules, PackageRule{
		MatchPackageNames:  matchPackageNames,
		GroupName:          fmt.Sprintf("Component Update %s", buildResult.Component.Name),
		BranchName:         fmt.Sprintf("konflux/component-updates/%s", buildResult.Component.Name),
		CommitMessageTopic: buildResult.Component.Name,
		PRFooter:           "To execute skipped test pipelines write comment `/ok-to-test`",
		RecreateWhen:       "always",
		RebaseWhen:         "behind-base-branch",
		Enabled:            true,
		FollowTag:          buildResult.BuiltImageTag,
	})

	renovateConfig := RenovateConfig{
		GitProvider:   target.GitProvider,
		Username:      target.Username,
		GitAuthor:     target.GitAuthor,
		Onboarding:    false,
		RequireConfig: "ignored",
		Repositories:  target.Repositories,
		// was 'regex' before but because: https://docs.renovatebot.com/configuration-options/#enabledmanagers
		EnabledManagers:     []string{"custom.regex"},
		Endpoint:            target.Endpoint,
		CustomManagers:      customManagers,
		RegistryAliases:     registryAliases,
		PackageRules:        packageRules,
		ForkProcessing:      "enabled",
		Extends:             []string{":gitSignOff"},
		DependencyDashboard: false,
		Labels:              []string{"konflux-nudge"},
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
func (u ComponentDependenciesUpdater) CreateRenovaterPipeline(ctx context.Context, namespace string, targets []updateTarget, debug bool, buildResult *BuildResult) error {
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
	globalConfigString := ""
	globalConfigType := ""

	allUserConfigMaps := &corev1.ConfigMapList{}
	if err := u.Client.List(ctx, allUserConfigMaps, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list config maps in %s namespace: %w", namespace, err)
	}
	for _, userConfigMap := range allUserConfigMaps.Items {
		if userConfigMap.Name == NamespaceWideRenovateConfigName {
			globalConfigString, globalConfigType = getConfigAndTypeFromConfigMap(userConfigMap)
			break
		}
	}

	for _, target := range targets {
		randomStr1 := RandomString(5)
		randomStr2 := RandomString(10)
		randomStr3 := RandomString(10)
		secretTokens[randomStr2] = target.Token
		secretTokens[randomStr3] = target.ImageRepositoryPassword
		componentConfigName := fmt.Sprintf("%s%s", ComponentRenovateConfigNamePrefix, target.ComponentName)
		componentConfigString := ""
		componentConfigType := ""
		configString := ""
		configType := "json"

		for _, userConfigMap := range allUserConfigMaps.Items {
			if userConfigMap.Name == componentConfigName {
				componentConfigString, componentConfigType = getConfigAndTypeFromConfigMap(userConfigMap)
				break
			}
		}

		if componentConfigString != "" {
			configString = componentConfigString
			configType = componentConfigType
			log.Info("will use custom renovate config for component", "name", componentConfigName, "type", configType)
		} else if globalConfigString != "" {
			configString = globalConfigString
			configType = globalConfigType
			log.Info("will use custom global renovate config", "name", NamespaceWideRenovateConfigName, "type", configType)
		} else {
			log.Info("will generate renovate config, no custom ones are present")
			renovateConfig, err := GenerateRenovateConfigForNudge(target, buildResult)
			if err != nil {
				return err
			}

			config, err := json.Marshal(renovateConfig)
			if err != nil {
				return err
			}
			configString = string(config)
		}

		log.Info(fmt.Sprintf("Creating renovate config map entry for %s component with length %d and value %s", target.ComponentName, len(configString), configString))

		configmaps[fmt.Sprintf("%s-%s.%s", target.ComponentName, randomStr1, configType)] = configString
		hostRules := fmt.Sprintf("\"[{'matchHost':'%s','username':'%s','password':'${TOKEN_%s}'}]\"", target.ImageRepositoryHost, target.ImageRepositoryUsername, randomStr3)

		// we are passing host rules via variable, because we can't resolve variable in json config
		// also this way we can use custom provided config without any modifications
		renovateCmds = append(renovateCmds,
			fmt.Sprintf("RENOVATE_PR_HOURLY_LIMIT=0 RENOVATE_PR_CONCURRENT_LIMIT=0 RENOVATE_TOKEN=$TOKEN_%s RENOVATE_CONFIG_FILE=/configs/%s-%s.%s RENOVATE_HOST_RULES=%s renovate", randomStr2, target.ComponentName, randomStr1, configType, hostRules),
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

func getConfigAndTypeFromConfigMap(configMap corev1.ConfigMap) (string, string) {
	config, exists := configMap.Data[ConfigKeyJson]
	if exists && len(config) > 0 {
		return config, "json"
	}

	config, exists = configMap.Data[ConfigKeyJs]
	if exists && len(config) > 0 {
		return config, "js"
	}

	return "", ""
}
