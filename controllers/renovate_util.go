package controllers

import (
	"context"
	"fmt"
	"github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	"github.com/redhat-appstudio/application-service/gitops/prepare"
	"github.com/redhat-appstudio/build-service/pkg/git/github"
	"github.com/redhat-appstudio/build-service/pkg/logs"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v13 "k8s.io/api/batch/v1"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logger "sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
	"time"
)

// GetAllGithubInstallations gets installations by iterating over every installation, should be used for cases where we expect to be touching most installed repos
func GetAllGithubInstallations(ctx context.Context, client client.Client, eventRecorder record.EventRecorder, componentList []v1alpha1.Component) (string, []installationStruct, error) {
	log := logger.FromContext(ctx)
	// Check if GitHub Application is used, if not then skip
	pacSecret := v1.Secret{}
	globalPaCSecretKey := types.NamespacedName{Namespace: buildServiceNamespaceName, Name: prepare.PipelinesAsCodeSecretName}
	if err := client.Get(ctx, globalPaCSecretKey, &pacSecret); err != nil {
		eventRecorder.Event(&pacSecret, "Warning", "ErrorReadingPaCSecret", err.Error())
		if errors.IsNotFound(err) {
			log.Error(err, "not found Pipelines as Code secret", "secret", prepare.PipelinesAsCodeSecretName, "namespace", buildServiceNamespaceName, logs.Action, logs.ActionView)
		} else {
			log.Error(err, "failed to get Pipelines as Code secret", "secret", prepare.PipelinesAsCodeSecretName, "namespace", buildServiceNamespaceName, logs.Action, logs.ActionView)
		}
		return "", nil, nil
	}
	isApp := gitops.IsPaCApplicationConfigured("github", pacSecret.Data)
	if !isApp {
		log.Info("GitHub App is not set")
		return "", nil, nil
	}

	// Load GitHub App and get GitHub Installations
	githubAppIdStr := string(pacSecret.Data[gitops.PipelinesAsCode_githubAppIdKey])
	privateKey := pacSecret.Data[gitops.PipelinesAsCode_githubPrivateKey]
	githubAppInstallations, slug, err := github.GetAllAppInstallations(githubAppIdStr, privateKey)
	if err != nil {
		return "", nil, err
	}

	componentUrlToBranchesMap := make(map[string][]string)
	for _, component := range componentList {
		gitSource := component.Spec.Source.GitSource
		if gitSource != nil {
			url := strings.TrimSuffix(strings.TrimSuffix(gitSource.URL, ".git"), "/")
			branch := gitSource.Revision
			if branch == "" {
				branch = InternalDefaultBranch
			}
			componentUrlToBranchesMap[url] = append(componentUrlToBranchesMap[url], branch)
		}
	}

	// Match installed repositories with Components and get custom branch if defined
	installationsToUpdate := []installationStruct{}
	for _, githubAppInstallation := range githubAppInstallations {
		repositories := []renovateRepository{}
		for _, repository := range githubAppInstallation.Repositories {
			branches, ok := componentUrlToBranchesMap[repository.GetHTMLURL()]
			// Filter repositories with installed GH App but missing Component
			if !ok {
				continue
			}
			for i := range branches {
				if branches[i] == InternalDefaultBranch {
					branches[i] = repository.GetDefaultBranch()
				}
			}

			repositories = append(repositories, renovateRepository{
				BaseBranches: branches,
				Repository:   repository.GetFullName(),
			})
		}
		// Do not add intatallation which has no matching repositories
		if len(repositories) == 0 {
			continue
		}
		installationsToUpdate = append(installationsToUpdate,
			installationStruct{
				id:           int(githubAppInstallation.ID),
				token:        githubAppInstallation.Token,
				repositories: repositories,
			})
	}
	return slug, installationsToUpdate, nil
}

// GetGithubInstallationsForComponents This method avoids iterating over all installations, it is intended to be called when the component list is small
func GetGithubInstallationsForComponents(ctx context.Context, client client.Client, eventRecorder record.EventRecorder, componentList []v1alpha1.Component) (string, []installationStruct, error) {
	log := logger.FromContext(ctx)
	// Check if GitHub Application is used, if not then skip
	pacSecret := v1.Secret{}
	globalPaCSecretKey := types.NamespacedName{Namespace: buildServiceNamespaceName, Name: prepare.PipelinesAsCodeSecretName}
	if err := client.Get(ctx, globalPaCSecretKey, &pacSecret); err != nil {
		eventRecorder.Event(&pacSecret, "Warning", "ErrorReadingPaCSecret", err.Error())
		if errors.IsNotFound(err) {
			log.Error(err, "not found Pipelines as Code secret in %s namespace: %w", globalPaCSecretKey.Namespace, err, logs.Action, logs.ActionView)
		} else {
			log.Error(err, "failed to get Pipelines as Code secret in %s namespace: %w", globalPaCSecretKey.Namespace, err, logs.Action, logs.ActionView)
		}
		return "", nil, nil
	}
	isApp := gitops.IsPaCApplicationConfigured("github", pacSecret.Data)
	if !isApp {
		log.Info("GitHub App is not set")
		return "", nil, nil
	}

	// Load GitHub App and get GitHub Installations
	githubAppIdStr := string(pacSecret.Data[gitops.PipelinesAsCode_githubAppIdKey])
	privateKey := pacSecret.Data[gitops.PipelinesAsCode_githubPrivateKey]

	// Match installed repositories with Components and get custom branch if defined
	installationsToUpdate := []installationStruct{}
	var slug string
	for _, component := range componentList {
		if component.Spec.Source.GitSource == nil {
			continue
		}

		gitSource := component.Spec.Source.GitSource

		url := strings.TrimSuffix(strings.TrimSuffix(gitSource.URL, ".git"), "/")
		githubAppInstallation, slugTmp, err := github.GetAppInstallationsForRepository(githubAppIdStr, privateKey, url)
		if slug == "" {
			slug = slugTmp
		}
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to get GitHub app installation for component %s/%s", component.Namespace, component.Name))
			continue
		}

		branch := gitSource.Revision
		if branch == "" {
			branch = InternalDefaultBranch
		}

		repositories := []renovateRepository{}
		for _, repository := range githubAppInstallation.Repositories {
			if branch == InternalDefaultBranch {
				branch = repository.GetDefaultBranch()
			}

			repositories = append(repositories, renovateRepository{
				BaseBranches: []string{branch},
				Repository:   repository.GetFullName(),
			})
		}
		// Do not add intatallation which has no matching repositories
		if len(repositories) == 0 {
			continue
		}
		installationsToUpdate = append(installationsToUpdate,
			installationStruct{
				id:           int(githubAppInstallation.ID),
				token:        githubAppInstallation.Token,
				repositories: repositories,
			})
	}

	return slug, installationsToUpdate, nil
}

// CreateRenovaterJob will create a renovate job in the system namespace to update RHTAP components
func CreateRenovaterJob(ctx context.Context, client client.Client, scheme *runtime.Scheme, installations []installationStruct, slug string, debug bool, js func(slug string, repositories []renovateRepository, info interface{}) (string, error), info interface{}) error {
	log := logger.FromContext(ctx)
	log.Info(fmt.Sprintf("Creating renovate job for %d installations", len(installations)))

	if len(installations) == 0 {
		return nil
	}
	timestamp := time.Now().Unix()
	name := fmt.Sprintf("renovate-job-%d-%s", timestamp, getRandomString(5))
	secretTokens := map[string]string{}
	configmaps := map[string]string{}
	renovateCmds := []string{}
	for _, installation := range installations {
		secretTokens[fmt.Sprint(installation.id)] = installation.token
		config, err := js(slug, installation.repositories, info)
		if err != nil {
			return err
		}
		configmaps[fmt.Sprintf("%d.js", installation.id)] = config

		log.Info(fmt.Sprintf("Creating renovate config map entry for %d installation with length %d and value %s", installation.id, len(config), config))
		renovateCmds = append(renovateCmds,
			fmt.Sprintf("RENOVATE_TOKEN=$TOKEN_%d RENOVATE_CONFIG_FILE=/configs/%d.js renovate", installation.id, installation.id),
		)
	}
	if len(renovateCmds) == 0 {
		return nil
	}
	secret := &v1.Secret{
		ObjectMeta: v12.ObjectMeta{
			Name:      name,
			Namespace: buildServiceNamespaceName,
		},
		StringData: secretTokens,
	}
	configMap := &v1.ConfigMap{
		ObjectMeta: v12.ObjectMeta{
			Name:      name,
			Namespace: buildServiceNamespaceName,
		},
		Data: configmaps,
	}
	trueBool := true
	falseBool := false
	backoffLimit := int32(1)
	timeToLive := int32(TimeToLiveOfJob.Seconds())
	renovateImageUrl := os.Getenv(RenovateImageEnvName)
	if renovateImageUrl == "" {
		renovateImageUrl = DefaultRenovateImageUrl
	}
	job := &v13.Job{
		ObjectMeta: v12.ObjectMeta{
			Name:      name,
			Namespace: buildServiceNamespaceName,
		},
		Spec: v13.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &timeToLive,
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Volumes: []v1.Volume{
						{
							Name: name,
							VolumeSource: v1.VolumeSource{
								ConfigMap: &v1.ConfigMapVolumeSource{
									LocalObjectReference: v1.LocalObjectReference{Name: name},
								},
							},
						},
					},
					Containers: []v1.Container{
						{
							Name:  "renovate",
							Image: renovateImageUrl,
							EnvFrom: []v1.EnvFromSource{
								{
									Prefix: "TOKEN_",
									SecretRef: &v1.SecretEnvSource{
										LocalObjectReference: v1.LocalObjectReference{
											Name: name,
										},
									},
								},
							},
							Command: []string{"bash", "-c", strings.Join(renovateCmds, "; ")},
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      name,
									MountPath: "/configs",
								},
							},
							SecurityContext: &v1.SecurityContext{
								Capabilities:             &v1.Capabilities{Drop: []v1.Capability{"ALL"}},
								RunAsNonRoot:             &trueBool,
								AllowPrivilegeEscalation: &falseBool,
								SeccompProfile: &v1.SeccompProfile{
									Type: v1.SeccompProfileTypeRuntimeDefault,
								},
							},
						},
					},
					RestartPolicy: v1.RestartPolicyNever,
				},
			},
		},
	}
	if debug {
		job.Spec.Template.Spec.Containers[0].Env = append(job.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "LOG_LEVEL", Value: "debug"})
	}
	if err := client.Create(ctx, secret); err != nil {
		return err
	}
	if err := client.Create(ctx, configMap); err != nil {
		return err
	}
	if err := client.Create(ctx, job); err != nil {
		return err
	}
	log.Info(fmt.Sprintf("Job %s triggered", job.Name), logs.Action, logs.ActionAdd)
	if err := controllerutil.SetOwnerReference(job, secret, scheme); err != nil {
		return err
	}
	if err := client.Update(ctx, secret); err != nil {
		return err
	}

	if err := controllerutil.SetOwnerReference(job, configMap, scheme); err != nil {
		return err
	}
	if err := client.Update(ctx, configMap); err != nil {
		return err
	}

	return nil
}

// CreateRenovaterPipeline will create a renovate pipeline in the user namespace, to update component dependencies.
// The reasons for using a pipeline in the component namespace instead of a Job in the system namespace is as follows:
// - The user namespace has direct access to secrets to allow updating private images
// - Job's are removed after a timeout, so lots of nudges in a short period could make the namespace unusable due to pod Quota, while pipelines are pruned much more aggressively
// - Users can view the results of pipelines and the results are stored, making debugging much easier
// - Tekton automatically provides docker config from linked service accounts for private images, with a job I would need to implement this manually
//
// Warning: the installation token used here should only be scoped to the individual repositories being updated
func CreateRenovaterPipeline(ctx context.Context, client client.Client, scheme *runtime.Scheme, namespace string, installations []installationStruct, slug string, debug bool, js func(slug string, repositories []renovateRepository, info interface{}) (string, error), info interface{}) error {
	log := logger.FromContext(ctx)
	log.Info(fmt.Sprintf("Creating renovate pipeline for %d installations", len(installations)))

	if len(installations) == 0 {
		return nil
	}
	timestamp := time.Now().Unix()
	name := fmt.Sprintf("renovate-pipeline-%d-%s", timestamp, getRandomString(5))
	secretTokens := map[string]string{}
	configmaps := map[string]string{}
	renovateCmds := []string{}
	for _, installation := range installations {
		secretTokens[fmt.Sprint(installation.id)] = installation.token
		config, err := js(slug, installation.repositories, info)
		if err != nil {
			return err
		}
		configmaps[fmt.Sprintf("%d.js", installation.id)] = config

		log.Info(fmt.Sprintf("Creating renovate config map entry for %d installation with length %d and value %s", installation.id, len(config), config))
		renovateCmds = append(renovateCmds,
			fmt.Sprintf("RENOVATE_TOKEN=$TOKEN_%d RENOVATE_CONFIG_FILE=/configs/%d.js renovate", installation.id, installation.id),
		)
	}
	if len(renovateCmds) == 0 {
		return nil
	}
	secret := &v1.Secret{
		ObjectMeta: v12.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		StringData: secretTokens,
	}
	configMap := &v1.ConfigMap{
		ObjectMeta: v12.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: configmaps,
	}
	trueBool := true
	falseBool := false
	renovateImageUrl := os.Getenv(RenovateImageEnvName)
	if renovateImageUrl == "" {
		renovateImageUrl = DefaultRenovateImageUrl
	}
	pipelineRun := &tektonapi.PipelineRun{
		ObjectMeta: v12.ObjectMeta{
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
								EnvFrom: []v1.EnvFromSource{
									{
										Prefix: "TOKEN_",
										SecretRef: &v1.SecretEnvSource{
											LocalObjectReference: v1.LocalObjectReference{
												Name: name,
											},
										},
									},
								},
								Command: []string{"bash", "-c", strings.Join(renovateCmds, "; ")},
								VolumeMounts: []v1.VolumeMount{
									{
										Name:      name,
										MountPath: "/configs",
									},
								},
								SecurityContext: &v1.SecurityContext{
									Capabilities:             &v1.Capabilities{Drop: []v1.Capability{"ALL"}},
									RunAsNonRoot:             &trueBool,
									AllowPrivilegeEscalation: &falseBool,
									SeccompProfile: &v1.SeccompProfile{
										Type: v1.SeccompProfileTypeRuntimeDefault,
									},
								},
							}},
							Volumes: []v1.Volume{
								{
									Name: name,
									VolumeSource: v1.VolumeSource{
										ConfigMap: &v1.ConfigMapVolumeSource{
											LocalObjectReference: v1.LocalObjectReference{Name: name},
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
	if debug {
		pipelineRun.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Env = append(pipelineRun.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Env, v1.EnvVar{Name: "LOG_LEVEL", Value: "debug"})
	}

	if err := client.Create(ctx, pipelineRun); err != nil {
		return err
	}
	// We create the PipelineRun first, and it will wait for the secret and configmap to be created
	if err := controllerutil.SetOwnerReference(pipelineRun, configMap, scheme); err != nil {
		return err
	}
	if err := controllerutil.SetOwnerReference(pipelineRun, secret, scheme); err != nil {
		return err
	}
	if err := client.Create(ctx, secret); err != nil {
		return err
	}
	if err := client.Create(ctx, configMap); err != nil {
		return err
	}
	log.Info(fmt.Sprintf("Pipeline %s triggered", pipelineRun.Name), logs.Action, logs.ActionAdd)

	return nil
}
