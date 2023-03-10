/*
Copyright 2021-2023 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	"github.com/redhat-appstudio/application-service/pkg/devfile"
	buildappstudiov1alpha1 "github.com/redhat-appstudio/build-service/api/v1alpha1"
	"github.com/redhat-appstudio/build-service/pkg/boerrors"
	"github.com/redhat-appstudio/build-service/pkg/github"
	"github.com/redhat-appstudio/build-service/pkg/gitlab"
	pipelineselector "github.com/redhat-appstudio/build-service/pkg/pipeline-selector"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	oci "github.com/tektoncd/pipeline/pkg/remote/oci"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"
)

const (
	pipelineRunOnPushSuffix    = "-on-push"
	pipelineRunOnPRSuffix      = "-on-pull-request"
	pipelineRunOnPushFilename  = "push.yaml"
	pipelineRunOnPRFilename    = "pull-request.yaml"
	pipelinesAsCodeNamespace   = "pipelines-as-code"
	pipelinesAsCodeRouteName   = "pipelines-as-code-controller"
	pipelinesAsCodeRouteEnvVar = "PAC_WEBHOOK_URL"

	defaultPipelineName   = "docker-build"
	defaultPipelineBundle = "quay.io/redhat-appstudio-tekton-catalog/pipeline-docker-build:8cf8982d58a841922b687b7166f0cfdc1cc3fc72"
)

// ProvisionPaCForComponent does Pipelines as Code provision for the given component.
// Mainly, it creates PaC configuration merge request into the component source repositotiry.
// If GitHub PaC application is not configured, creates a webhook for PaC.
func (r *ComponentBuildReconciler) ProvisionPaCForComponent(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := r.Log.WithValues("ComponentOnboardingForPaC", types.NamespacedName{Namespace: component.Namespace, Name: component.Name})

	gitProvider, err := gitops.GetGitProvider(*component)
	if err != nil {
		// Do not reconcile, because configuration must be fixed before it is possible to proceed.
		return boerrors.NewBuildOpError(boerrors.EUnknownGitProvider,
			fmt.Errorf("error detecting git provider: %w", err))
	}

	pacSecret, err := r.ensurePaCSecret(ctx, component, gitProvider)
	if err != nil {
		return err
	}

	if err := validatePaCConfiguration(gitProvider, pacSecret.Data); err != nil {
		r.EventRecorder.Event(pacSecret, "Warning", "ErrorValidatingPaCSecret", err.Error())
		// Do not reconcile, because configuration must be fixed before it is possible to proceed.
		return boerrors.NewBuildOpError(boerrors.EPaCSecretInvalid,
			fmt.Errorf("invalid configuration in Pipelines as Code secret: %w", err))
	}

	var webhookSecretString, webhookTargetUrl string
	if !gitops.IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
		// Generate webhook secret for the component git repository if not yet generated
		// and stores it in the corresponding k8s secret.
		webhookSecretString, err = r.ensureWebhookSecret(ctx, component)
		if err != nil {
			return err
		}

		// Obtain Pipelines as Code callback URL
		webhookTargetUrl, err = r.getPaCWebhookTargetUrl(ctx)
		if err != nil {
			return err
		}
	}

	if err := r.ensurePaCRepository(ctx, component, pacSecret.Data); err != nil {
		return err
	}

	// Manage merge request for Pipelines as Code configuration
	mrUrl, err := r.ConfigureRepositoryForPaC(ctx, component, pacSecret.Data, webhookTargetUrl, webhookSecretString)
	if err != nil {
		r.EventRecorder.Event(component, "Warning", "ErrorConfiguringPaCForComponentRepository", err.Error())
		return err
	}
	var mrMessage string
	if mrUrl != "" {
		mrMessage = fmt.Sprintf("Pipelines as Code configuration merge request: %s", mrUrl)
	} else {
		mrMessage = "Pipelines as Code configuration is up to date"
	}
	log.Info(mrMessage)
	r.EventRecorder.Event(component, "Normal", "PipelinesAsCodeConfiguration", mrMessage)

	if mrUrl != "" {
		// PaC PR has been just created
		pipelinesAsCodeComponentProvisionTimeMetric.Observe(time.Since(component.CreationTimestamp.Time).Seconds())
	}

	return nil
}

// UndoPaCProvisionForComponent creates merge request that removes Pipelines as Code configuration from component source repository.
// Deletes PaC webhook if used.
// In case of any errors just logs them and does not block Component deletion.
func (r *ComponentBuildReconciler) UndoPaCProvisionForComponent(ctx context.Context, component *appstudiov1alpha1.Component) {
	log := r.Log.WithValues("ComponentPaCCleanup", types.NamespacedName{Namespace: component.Namespace, Name: component.Name})

	gitProvider, err := gitops.GetGitProvider(*component)
	if err != nil {
		log.Error(err, "error detecting git provider")
		// There is no point to continue if git provider is not known.
		return
	}

	pacSecret := corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: buildServiceNamespaceName, Name: gitopsprepare.PipelinesAsCodeSecretName}, &pacSecret); err != nil {
		log.Error(err, "error getting git provider credentials secret")
		// Cannot continue without accessing git provider credentials.
		return
	}

	webhookTargetUrl := ""
	if !gitops.IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
		webhookTargetUrl, err = r.getPaCWebhookTargetUrl(ctx)
		if err != nil {
			// Just log the error and continue with merge request creation
			log.Error(err, "failed to get Pipelines as Code webhook target URL")
		}
	}

	// Manage merge request for Pipelines as Code configuration removal
	mrUrl, err := r.UnconfigureRepositoryForPaC(log, component, pacSecret.Data, webhookTargetUrl)
	if err != nil {
		log.Error(err, "failed to create merge request to remove Pipelines as Code configuration from Component source repository")
		return
	}
	var mrMessage string
	if mrUrl != "" {
		mrMessage = fmt.Sprintf("Pipelines as Code configuration removal merge request: %s", mrUrl)
	} else {
		mrMessage = "Pipelines as Code configuration removal merge request is not needed"
	}
	log.Info(mrMessage)
}

func (r *ComponentBuildReconciler) ensurePaCSecret(ctx context.Context, component *appstudiov1alpha1.Component, gitProvider string) (*corev1.Secret, error) {
	// Expected that the secret contains token for Pipelines as Code webhook configuration,
	// but under <git-provider>.token field. For example: github.token
	// Also it can contain github-private-key and github-application-id
	// in case GitHub Application is used instead of webhook.
	pacSecret := corev1.Secret{}
	pacSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: gitopsprepare.PipelinesAsCodeSecretName}
	if err := r.Client.Get(ctx, pacSecretKey, &pacSecret); err != nil {
		if !errors.IsNotFound(err) {
			r.EventRecorder.Event(&pacSecret, "Warning", "ErrorReadingPaCSecret", err.Error())
			return nil, fmt.Errorf("failed to get Pipelines as Code secret in %s namespace: %w", component.Namespace, err)
		}

		// Fallback to the global configuration
		globalPaCSecretKey := types.NamespacedName{Namespace: buildServiceNamespaceName, Name: gitopsprepare.PipelinesAsCodeSecretName}
		if err := r.Client.Get(ctx, globalPaCSecretKey, &pacSecret); err != nil {
			if !errors.IsNotFound(err) {
				r.EventRecorder.Event(&pacSecret, "Warning", "ErrorReadingPaCSecret", err.Error())
				return nil, fmt.Errorf("failed to get Pipelines as Code secret in %s namespace: %w", globalPaCSecretKey.Namespace, err)
			}

			r.EventRecorder.Event(&pacSecret, "Warning", "PaCSecretNotFound", err.Error())
			// Do not trigger a new reconcile. The PaC secret must be created first.
			return nil, boerrors.NewBuildOpError(boerrors.EPaCSecretNotFound,
				fmt.Errorf(" Pipelines as Code secret not found in %s namespace nor in %s", pacSecretKey.Namespace, globalPaCSecretKey.Namespace))
		}

		if !gitops.IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
			// Webhook is used. We need to reference access token in the component namespace.
			// Copy global PaC configuration in component namespace
			localPaCSecret := &corev1.Secret{
				TypeMeta: pacSecret.TypeMeta,
				ObjectMeta: metav1.ObjectMeta{
					Name:      pacSecretKey.Name,
					Namespace: pacSecretKey.Namespace,
					Labels: map[string]string{
						PartOfLabelName: PartOfAppStudioLabelValue,
					},
				},
				Data: pacSecret.Data,
			}
			if err := r.Client.Create(ctx, localPaCSecret); err != nil {
				return nil, fmt.Errorf("failed to create local PaC configuration secret: %w", err)
			}
		}
	}

	return &pacSecret, nil
}

// Returns webhook secret for given component.
// Generates the webhook secret and saves it the k8s secret if doesn't exist.
func (r *ComponentBuildReconciler) ensureWebhookSecret(ctx context.Context, component *appstudiov1alpha1.Component) (string, error) {
	webhookSecretsSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: gitops.PipelinesAsCodeWebhooksSecretName, Namespace: component.GetNamespace()}, webhookSecretsSecret); err != nil {
		if errors.IsNotFound(err) {
			webhookSecretsSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      gitops.PipelinesAsCodeWebhooksSecretName,
					Namespace: component.GetNamespace(),
					Labels: map[string]string{
						PartOfLabelName: PartOfAppStudioLabelValue,
					},
				},
			}
			if err := r.Client.Create(ctx, webhookSecretsSecret); err != nil {
				r.Log.Error(err, "failed to create webhooks secrets secret")
				return "", err
			}
			return r.ensureWebhookSecret(ctx, component)
		}

		r.Log.Error(err, "failed to get webhook secrets secret")
		return "", err
	}

	componentWebhookSecretKey := gitops.GetWebhookSecretKeyForComponent(*component)
	if _, exists := webhookSecretsSecret.Data[componentWebhookSecretKey]; exists {
		// The webhook secret already exists. Use single secret for the same repository.
		return string(webhookSecretsSecret.Data[componentWebhookSecretKey]), nil
	}

	webhookSecretString := generatePaCWebhookSecretString()

	if webhookSecretsSecret.Data == nil {
		webhookSecretsSecret.Data = make(map[string][]byte)
	}
	webhookSecretsSecret.Data[componentWebhookSecretKey] = []byte(webhookSecretString)
	if err := r.Client.Update(ctx, webhookSecretsSecret); err != nil {
		r.Log.Error(err, "failed to update webhook secrets secret")
		return "", err
	}

	return webhookSecretString, nil
}

// generatePaCWebhookSecretString generates string alike openssl rand -hex 20
func generatePaCWebhookSecretString() string {
	length := 20 // in bytes
	tokenBytes := make([]byte, length)
	if _, err := rand.Read(tokenBytes); err != nil {
		panic("Failed to read from random generator")
	}
	return hex.EncodeToString(tokenBytes)
}

// getPaCWebhookTargetUrl returns URL to which events from git repository should be sent.
func (r *ComponentBuildReconciler) getPaCWebhookTargetUrl(ctx context.Context) (string, error) {
	webhookTargetUrl := os.Getenv(pipelinesAsCodeRouteEnvVar)
	if webhookTargetUrl == "" {
		// The env variable is not set
		// Use the installed on the cluster Pipelines as Code
		var err error
		webhookTargetUrl, err = r.getPaCRoutePublicUrl(ctx)
		if err != nil {
			return "", err
		}
	}
	return webhookTargetUrl, nil
}

// getPaCRoutePublicUrl returns Pipelines as Code public route that recieves events to trigger new pipeline runs.
func (r *ComponentBuildReconciler) getPaCRoutePublicUrl(ctx context.Context) (string, error) {
	pacWebhookRoute := &routev1.Route{}
	pacWebhookRouteKey := types.NamespacedName{Namespace: pipelinesAsCodeNamespace, Name: pipelinesAsCodeRouteName}
	if err := r.Client.Get(ctx, pacWebhookRouteKey, pacWebhookRoute); err != nil {
		return "", fmt.Errorf("failed to get Pipelines as Code route: %w", err)
	}
	return "https://" + pacWebhookRoute.Spec.Host, nil
}

// validatePaCConfiguration detects checks that all required fields is set for whatever method is used.
func validatePaCConfiguration(gitProvider string, config map[string][]byte) error {
	isApp := gitops.IsPaCApplicationConfigured(gitProvider, config)

	expectedPaCWebhookConfigFields := []string{gitops.GetProviderTokenKey(gitProvider)}

	var err error
	switch gitProvider {
	case "github":
		if isApp {
			// GitHub application

			err = checkMandatoryFieldsNotEmpty(config, []string{gitops.PipelinesAsCode_githubAppIdKey, gitops.PipelinesAsCode_githubPrivateKey})
			if err != nil {
				break
			}

			// validate content of the fields
			if _, e := strconv.ParseInt(string(config[gitops.PipelinesAsCode_githubAppIdKey]), 10, 64); e != nil {
				err = fmt.Errorf(" Pipelines as Code: failed to parse GitHub application ID. Cause: %w", e)
				break
			}

			privateKey := strings.TrimSpace(string(config[gitops.PipelinesAsCode_githubPrivateKey]))
			if !strings.HasPrefix(privateKey, "-----BEGIN RSA PRIVATE KEY-----") ||
				!strings.HasSuffix(privateKey, "-----END RSA PRIVATE KEY-----") {
				err = fmt.Errorf(" Pipelines as Code secret: GitHub application private key is invalid")
				break
			}
		} else {
			// webhook
			err = checkMandatoryFieldsNotEmpty(config, expectedPaCWebhookConfigFields)
		}

	case "gitlab":
		err = checkMandatoryFieldsNotEmpty(config, expectedPaCWebhookConfigFields)

	case "bitbucket":
		err = checkMandatoryFieldsNotEmpty(config, []string{gitops.GetProviderTokenKey(gitProvider)})
		if err != nil {
			break
		}

		if len(config["username"]) == 0 {
			err = fmt.Errorf(" Pipelines as Code secret: name of the user field must be configured")
		}

	default:
		err = fmt.Errorf("unsupported git provider: %s", gitProvider)
	}

	return err
}

func checkMandatoryFieldsNotEmpty(config map[string][]byte, mandatoryFields []string) error {
	for _, field := range mandatoryFields {
		if len(config[field]) == 0 {
			return fmt.Errorf(" Pipelines as Code secret: %s field is not configured", field)
		}
	}
	return nil
}

func (r *ComponentBuildReconciler) ensurePaCRepository(ctx context.Context, component *appstudiov1alpha1.Component, config map[string][]byte) error {
	repository, err := gitops.GeneratePACRepository(*component, config)
	if err != nil {
		return err
	}

	existingRepository := &pacv1alpha1.Repository{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: repository.Name, Namespace: repository.Namespace}, existingRepository); err != nil {
		if errors.IsNotFound(err) {
			if err := controllerutil.SetOwnerReference(component, repository, r.Scheme); err != nil {
				return err
			}
			if err := r.Client.Create(ctx, repository); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return nil
}

// generatePaCPipelineRunConfigs generates PipelineRun YAML configs for given component.
// The generated PipelineRun Yaml content are returned in byte string and in the order of push and pull request.
func (r *ComponentBuildReconciler) generatePaCPipelineRunConfigs(
	ctx context.Context, log logr.Logger, component *appstudiov1alpha1.Component, pacTargetBranch string) ([]byte, []byte, error) {

	pipelineRef, additionalPipelineParams, err := r.GetPipelineForComponent(ctx, component)
	if err != nil {
		return nil, nil, err
	}
	log.Info(fmt.Sprintf("Selected %s pipeline from %s bundle for %s component",
		pipelineRef.Name, pipelineRef.Bundle, component.Name))

	// Get pipeline from the bundle to be expanded to the PipelineRun
	pipelineSpec, err := retrievePipelineSpec(pipelineRef.Bundle, pipelineRef.Name)
	if err != nil {
		r.EventRecorder.Event(component, "Warning", "ErrorGettingPipelineFromBundle", err.Error())
		return nil, nil, err
	}

	pipelineRunOnPush, err := generatePaCPipelineRunForComponent(
		component, pipelineSpec, additionalPipelineParams, false, pacTargetBranch)
	if err != nil {
		return nil, nil, err
	}
	pipelineRunOnPushYaml, err := yaml.Marshal(pipelineRunOnPush)
	if err != nil {
		return nil, nil, err
	}

	pipelineRunOnPR, err := generatePaCPipelineRunForComponent(
		component, pipelineSpec, additionalPipelineParams, true, pacTargetBranch)
	if err != nil {
		return nil, nil, err
	}
	pipelineRunOnPRYaml, err := yaml.Marshal(pipelineRunOnPR)
	if err != nil {
		return nil, nil, err
	}

	return pipelineRunOnPushYaml, pipelineRunOnPRYaml, nil
}

// ConfigureRepositoryForPaC creates a merge request with initial Pipelines as Code configuration
// and configures a webhook to notify in-cluster PaC unless application (on the repository side) is used.
func (r *ComponentBuildReconciler) ConfigureRepositoryForPaC(ctx context.Context, component *appstudiov1alpha1.Component, config map[string][]byte, webhookTargetUrl, webhookSecret string) (prUrl string, err error) {
	log := r.Log.WithValues("Repository", component.Spec.Source.GitSource.URL)

	gitProvider, _ := gitops.GetGitProvider(*component)
	isAppUsed := gitops.IsPaCApplicationConfigured(gitProvider, config)

	var accessToken string
	if !isAppUsed {
		accessToken = strings.TrimSpace(string(config[gitops.GetProviderTokenKey(gitProvider)]))
	}

	// https://github.com/owner/repository
	gitSourceUrlParts := strings.Split(strings.TrimSuffix(component.Spec.Source.GitSource.URL, ".git"), "/")

	commitMessage := "Appstudio update " + component.Name
	branch := "appstudio-" + component.Name
	mrTitle := "Appstudio update " + component.Name
	mrText := "Pipelines as Code configuration proposal"
	authorName := "redhat-appstudio"
	authorEmail := "appstudio@redhat.com"

	var baseBranch string
	if component.Spec.Source.GitSource != nil {
		baseBranch = component.Spec.Source.GitSource.Revision
	}

	switch gitProvider {
	case "github":
		owner := gitSourceUrlParts[3]
		repository := gitSourceUrlParts[4]

		var ghclient *github.GithubClient
		if isAppUsed {
			githubAppIdStr := string(config[gitops.PipelinesAsCode_githubAppIdKey])
			githubAppId, err := strconv.ParseInt(githubAppIdStr, 10, 64)
			if err != nil {
				return "", fmt.Errorf("failed to convert %s to int: %w", githubAppIdStr, err)
			}

			privateKey := config[gitops.PipelinesAsCode_githubPrivateKey]
			ghclient, err = github.NewGithubClientByApp(githubAppId, privateKey, owner)
			if err != nil {
				return "", err
			}

			// Check if the application is installed into target repository
			appInstalled, err := github.IsAppInstalledIntoRepository(ghclient, owner, repository)
			if err != nil {
				return "", err
			}
			if !appInstalled {
				return "", boerrors.NewBuildOpError(boerrors.EGitHubAppNotInstalled, fmt.Errorf("GitHub Application is not installed into the repository"))
			}
		} else {
			// Webhook
			ghclient = github.NewGithubClient(accessToken)

			log.Info("Setup Pipelines as Code webhook for")
			err = github.SetupPaCWebhook(ghclient, webhookTargetUrl, webhookSecret, owner, repository)
			if err != nil {
				return "", err
			} else {
				r.Log.Info(fmt.Sprintf("Pipelines as Code webhook \"%s\" configured for %s component in %s namespace\n", webhookTargetUrl, component.GetName(), component.GetNamespace()))
			}
		}

		if baseBranch == "" {
			baseBranch, err = github.GetDefaultBranch(ghclient, owner, repository)
			if err != nil {
				return "", nil
			}
		}

		pipelineRunOnPushYaml, pipelineRunOnPRYaml, err := r.generatePaCPipelineRunConfigs(ctx, log, component, baseBranch)
		if err != nil {
			return "", err
		}
		prData := &github.PaCPullRequestData{
			Owner:         owner,
			Repository:    repository,
			CommitMessage: commitMessage,
			Branch:        branch,
			BaseBranch:    baseBranch,
			PRTitle:       mrTitle,
			PRText:        mrText,
			AuthorName:    authorName,
			AuthorEmail:   authorEmail,
			Files: []github.File{
				{FullPath: ".tekton/" + component.Name + "-" + pipelineRunOnPushFilename, Content: pipelineRunOnPushYaml},
				{FullPath: ".tekton/" + component.Name + "-" + pipelineRunOnPRFilename, Content: pipelineRunOnPRYaml},
			},
		}
		prUrl, err = github.CreatePaCPullRequest(ghclient, prData)
		if err != nil {
			// Handle case when GitHub application is not installed for the component repository
			if strings.Contains(err.Error(), "Resource not accessible by integration") {
				return "", fmt.Errorf(" Pipelines as Code GitHub application with %s ID is not installed for %s repository",
					string(config[gitops.PipelinesAsCode_githubAppIdKey]), component.Spec.Source.GitSource.URL)
			}
			return "", err
		}

		return prUrl, nil

	case "gitlab":
		glclient, err := gitlab.NewGitlabClient(accessToken)
		if err != nil {
			return "", err
		}

		gitlabNamespace := gitSourceUrlParts[3]
		gitlabProjectName := gitSourceUrlParts[4]
		projectPath := gitlabNamespace + "/" + gitlabProjectName

		err = gitlab.SetupPaCWebhook(glclient, projectPath, webhookTargetUrl, webhookSecret)
		if err != nil {
			return "", err
		}

		if baseBranch == "" {
			baseBranch, err = gitlab.GetDefaultBranch(glclient, projectPath)
			if err != nil {
				return "", nil
			}
		}

		pipelineRunOnPushYaml, pipelineRunOnPRYaml, err := r.generatePaCPipelineRunConfigs(ctx, log, component, baseBranch)
		if err != nil {
			return "", err
		}
		mrData := &gitlab.PaCMergeRequestData{
			ProjectPath:   projectPath,
			CommitMessage: commitMessage,
			Branch:        branch,
			BaseBranch:    baseBranch,
			MrTitle:       mrTitle,
			MrText:        mrText,
			AuthorName:    authorName,
			AuthorEmail:   authorEmail,
			Files: []gitlab.File{
				{FullPath: ".tekton/" + component.Name + "-" + pipelineRunOnPushFilename, Content: pipelineRunOnPushYaml},
				{FullPath: ".tekton/" + component.Name + "-" + pipelineRunOnPRFilename, Content: pipelineRunOnPRYaml},
			},
		}
		mrUrl, err := gitlab.EnsurePaCMergeRequest(glclient, mrData)
		return mrUrl, err

	case "bitbucket":
		// TODO implement
		return "", fmt.Errorf("git provider %s is not supported", gitProvider)
	default:
		return "", fmt.Errorf("git provider %s is not supported", gitProvider)
	}
}

// UnconfigureRepositoryForPaC creates a merge request that deletes Pipelines as Code configuration of the diven component in its repository.
// Deletes PaC webhook if it's used.
// Does not delete PaC GitHub application from the repository as its installation was done manually by the user.
// Returns merge request web URL or empty string if it's not needed.
func (r *ComponentBuildReconciler) UnconfigureRepositoryForPaC(log logr.Logger, component *appstudiov1alpha1.Component, config map[string][]byte, webhookTargetUrl string) (prUrl string, err error) {
	gitProvider, _ := gitops.GetGitProvider(*component)
	isAppUsed := gitops.IsPaCApplicationConfigured(gitProvider, config)

	var accessToken string
	if !isAppUsed {
		accessToken = strings.TrimSpace(string(config[gitops.GetProviderTokenKey(gitProvider)]))
	}

	// https://github.com/owner/repository
	gitSourceUrlParts := strings.Split(strings.TrimSuffix(component.Spec.Source.GitSource.URL, ".git"), "/")

	commitMessage := "Appstudio purge " + component.Name
	branch := "appstudio-purge-" + component.Name
	baseBranch := "" // empty string means autodetect
	mrTitle := "Appstudio purge " + component.Name
	mrText := "Pipelines as Code configuration removal"
	authorName := "redhat-appstudio"
	authorEmail := "appstudio@redhat.com"

	if component.Spec.Source.GitSource != nil && component.Spec.Source.GitSource.Revision != "" {
		baseBranch = component.Spec.Source.GitSource.Revision
	}

	switch gitProvider {
	case "github":
		owner := gitSourceUrlParts[3]
		repository := gitSourceUrlParts[4]

		var ghclient *github.GithubClient
		if isAppUsed {
			githubAppIdStr := string(config[gitops.PipelinesAsCode_githubAppIdKey])
			githubAppId, err := strconv.ParseInt(githubAppIdStr, 10, 64)
			if err != nil {
				return "", fmt.Errorf("failed to convert %s to int: %w", githubAppIdStr, err)
			}

			privateKey := config[gitops.PipelinesAsCode_githubPrivateKey]
			ghclient, err = github.NewGithubClientByApp(githubAppId, privateKey, owner)
			if err != nil {
				return "", err
			}
		} else {
			// Webhook
			ghclient = github.NewGithubClient(accessToken)

			if webhookTargetUrl != "" {
				err = github.DeletePaCWebhook(ghclient, webhookTargetUrl, owner, repository)
				if err != nil {
					// Just log the error and continue with merge request creation
					log.Error(err, "failed to delete Pipelines as Code webhook")
				} else {
					log.Info(fmt.Sprintf("Pipelines as Code webhook \"%s\" deleted for %s component in %s namespace\n", webhookTargetUrl, component.GetName(), component.GetNamespace()))
				}
			}
		}

		prData := &github.PaCPullRequestData{
			Owner:         owner,
			Repository:    repository,
			CommitMessage: commitMessage,
			Branch:        branch,
			BaseBranch:    baseBranch,
			PRTitle:       mrTitle,
			PRText:        mrText,
			AuthorName:    authorName,
			AuthorEmail:   authorEmail,
			Files: []github.File{
				{FullPath: ".tekton/" + component.Name + "-" + pipelineRunOnPushFilename},
				{FullPath: ".tekton/" + component.Name + "-" + pipelineRunOnPRFilename},
			},
		}
		prUrl, err = github.UndoPaCPullRequest(ghclient, prData)
		if err != nil {
			// Handle case when GitHub application is not installed for the component repository
			if strings.Contains(err.Error(), "Resource not accessible by integration") {
				return "", fmt.Errorf(" Pipelines as Code GitHub application with %s ID is not installed for %s repository",
					string(config[gitops.PipelinesAsCode_githubAppIdKey]), component.Spec.Source.GitSource.URL)
			}
			return "", err
		}

		return prUrl, nil

	case "gitlab":
		glclient, err := gitlab.NewGitlabClient(accessToken)
		if err != nil {
			return "", err
		}

		gitlabNamespace := gitSourceUrlParts[3]
		gitlabProjectName := gitSourceUrlParts[4]
		projectPath := gitlabNamespace + "/" + gitlabProjectName

		err = gitlab.DeletePaCWebhook(glclient, projectPath, webhookTargetUrl)
		if err != nil {
			// Just log the error and continue with merge request creation
			log.Error(err, "failed to delete Pipelines as Code webhook")
		}

		mrData := &gitlab.PaCMergeRequestData{
			ProjectPath:   projectPath,
			CommitMessage: commitMessage,
			Branch:        branch,
			BaseBranch:    baseBranch,
			MrTitle:       mrTitle,
			MrText:        mrText,
			AuthorName:    authorName,
			AuthorEmail:   authorEmail,
			Files: []gitlab.File{
				{FullPath: ".tekton/" + component.Name + "-" + pipelineRunOnPushFilename},
				{FullPath: ".tekton/" + component.Name + "-" + pipelineRunOnPRFilename},
			},
		}
		return gitlab.UndoPaCMergeRequest(glclient, mrData)

	case "bitbucket":
		// TODO implement
		return "", fmt.Errorf("git provider %s is not supported", gitProvider)
	default:
		return "", fmt.Errorf("git provider %s is not supported", gitProvider)
	}
}

// generatePaCPipelineRunForComponent returns pipeline run definition to build component source with.
// Generated pipeline run contains placeholders that are expanded by Pipeline-as-Code.
func generatePaCPipelineRunForComponent(
	component *appstudiov1alpha1.Component,
	pipelineSpec *tektonapi.PipelineSpec,
	additionalPipelineParams []tektonapi.Param,
	onPull bool,
	pacTargetBranch string) (*tektonapi.PipelineRun, error) {

	if pacTargetBranch == "" {
		return nil, fmt.Errorf("target branch can't be empty for generating PaC PipelineRun for: %v", component)
	}

	annotations := map[string]string{
		"pipelinesascode.tekton.dev/on-target-branch": "[" + pacTargetBranch + "]",
		"pipelinesascode.tekton.dev/max-keep-runs":    "3",
		"build.appstudio.redhat.com/commit_sha":       "{{revision}}",
		"build.appstudio.redhat.com/target_branch":    "{{target_branch}}",
	}
	labels := map[string]string{
		ApplicationNameLabelName:                component.Spec.Application,
		ComponentNameLabelName:                  component.Name,
		"pipelines.appstudio.openshift.io/type": "build",
	}

	imageRepo := getContainerImageRepository(component.Spec.ContainerImage)
	var pipelineName string
	var proposedImage string
	if onPull {
		annotations["pipelinesascode.tekton.dev/on-event"] = "[pull_request]"
		annotations["build.appstudio.redhat.com/pull_request_number"] = "{{pull_request_number}}"
		pipelineName = component.Name + pipelineRunOnPRSuffix
		proposedImage = imageRepo + ":on-pr-{{revision}}"
	} else {
		annotations["pipelinesascode.tekton.dev/on-event"] = "[push]"
		pipelineName = component.Name + pipelineRunOnPushSuffix
		proposedImage = imageRepo + ":{{revision}}"
	}

	params := []tektonapi.Param{
		{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "{{repo_url}}"}},
		{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "{{revision}}"}},
		{Name: "output-image", Value: tektonapi.ArrayOrString{Type: "string", StringVal: proposedImage}},
	}

	dockerFile, err := devfile.SearchForDockerfile([]byte(component.Status.Devfile))
	if err != nil {
		return nil, err
	}
	if dockerFile != nil {
		if dockerFile.Uri != "" {
			params = append(params, tektonapi.Param{Name: "dockerfile", Value: tektonapi.ArrayOrString{Type: "string", StringVal: dockerFile.Uri}})
		}
		pathContext := getPathContext(component.Spec.Source.GitSource.Context, dockerFile.BuildContext)
		if pathContext != "" {
			params = append(params, tektonapi.Param{Name: "path-context", Value: tektonapi.ArrayOrString{Type: "string", StringVal: pathContext}})
		}
	}

	params = mergeAndSortTektonParams(params, additionalPipelineParams)

	pipelineRunWorkspaces := createWorkspaceBinding(pipelineSpec.Workspaces)

	pipelineRun := &tektonapi.PipelineRun{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PipelineRun",
			APIVersion: "tekton.dev/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        pipelineName,
			Namespace:   component.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: tektonapi.PipelineRunSpec{
			PipelineSpec: pipelineSpec,
			Params:       params,
			Workspaces:   pipelineRunWorkspaces,
		},
	}

	return pipelineRun, nil
}

// getContainerImageRepository removes tag or SHA has from container image reference
func getContainerImageRepository(image string) string {
	if strings.Contains(image, "@") {
		// registry.io/user/image@sha256:586ab...d59a
		return strings.Split(image, "@")[0]
	}
	// registry.io/user/image:tag
	return strings.Split(image, ":")[0]
}

func getPathContext(gitContext, dockerfileContext string) string {
	if gitContext == "" && dockerfileContext == "" {
		return ""
	}
	separator := string(filepath.Separator)
	path := filepath.Join(gitContext, dockerfileContext)
	path = filepath.Clean(path)
	path = strings.TrimPrefix(path, separator)
	return path
}

func createWorkspaceBinding(pipelineWorkspaces []tektonapi.PipelineWorkspaceDeclaration) []tektonapi.WorkspaceBinding {
	pipelineRunWorkspaces := []tektonapi.WorkspaceBinding{}
	for _, workspace := range pipelineWorkspaces {
		switch workspace.Name {
		case "workspace":
			pipelineRunWorkspaces = append(pipelineRunWorkspaces,
				tektonapi.WorkspaceBinding{
					Name:                workspace.Name,
					VolumeClaimTemplate: generateVolumeClaimTemplate(),
				})
		case "git-auth":
			pipelineRunWorkspaces = append(pipelineRunWorkspaces,
				tektonapi.WorkspaceBinding{
					Name:   workspace.Name,
					Secret: &corev1.SecretVolumeSource{SecretName: "{{ git_auth_secret }}"},
				})
		}
	}
	return pipelineRunWorkspaces
}

func generateVolumeClaimTemplate() *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				"ReadWriteOnce",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": resource.MustParse("1Gi"),
				},
			},
		},
	}
}

// mergeAndSortTektonParams merges additional params into existing params by adding new or replacing existing values.
func mergeAndSortTektonParams(existedParams, additionalParams []tektonapi.Param) []tektonapi.Param {
	var params []tektonapi.Param
	paramsMap := make(map[string]tektonapi.Param)
	for _, p := range existedParams {
		paramsMap[p.Name] = p
	}
	for _, p := range additionalParams {
		paramsMap[p.Name] = p
	}
	for _, v := range paramsMap {
		params = append(params, v)
	}
	sort.Slice(params, func(i, j int) bool {
		return params[i].Name < params[j].Name
	})
	return params
}

// GetPipelineForComponent searches for the build pipeline to use on the component.
func (r *ComponentBuildReconciler) GetPipelineForComponent(ctx context.Context, component *appstudiov1alpha1.Component) (*tektonapi.PipelineRef, []tektonapi.Param, error) {
	var pipelineSelectors []buildappstudiov1alpha1.BuildPipelineSelector
	pipelineSelector := &buildappstudiov1alpha1.BuildPipelineSelector{}

	pipelineSelectorKeys := []types.NamespacedName{
		// First try specific config for the application
		{Namespace: component.Namespace, Name: component.Spec.Application},
		// Second try namespaced config
		{Namespace: component.Namespace, Name: buildPipelineSelectorResourceName},
		// Finally try global config
		{Namespace: buildServiceNamespaceName, Name: buildPipelineSelectorResourceName},
	}

	for _, pipelineSelectorKey := range pipelineSelectorKeys {
		if err := r.Client.Get(ctx, pipelineSelectorKey, pipelineSelector); err != nil {
			if !errors.IsNotFound(err) {
				return nil, nil, err
			}
			// The config is not found, try the next one in the hierarchy
		} else {
			pipelineSelectors = append(pipelineSelectors, *pipelineSelector)
		}
	}

	if len(pipelineSelectors) > 0 {
		pipelineRef, pipelineParams, err := pipelineselector.SelectPipelineForComponent(component, pipelineSelectors)
		if err != nil {
			return nil, nil, err
		}
		if pipelineRef != nil {
			return pipelineRef, pipelineParams, nil
		}
	}

	// Fallback to the default pipeline
	return &tektonapi.PipelineRef{
		Name:   defaultPipelineName,
		Bundle: defaultPipelineBundle,
	}, nil, nil
}

// retrievePipelineSpec retrieves pipeline definition with given name from the given bundle.
func retrievePipelineSpec(bundleUri, pipelineName string) (*tektonapi.PipelineSpec, error) {
	var obj runtime.Object
	var err error
	resolver := oci.NewResolver(bundleUri, authn.DefaultKeychain)

	if obj, _, err = resolver.Get(context.TODO(), "pipeline", pipelineName); err != nil {
		return nil, err
	}
	pipelineSpecObj, ok := obj.(tektonapi.PipelineObject)
	if !ok {
		return nil, fmt.Errorf("failed to extract pipeline %s from bundle %s", bundleUri, pipelineName)
	}
	pipelineSpec := pipelineSpecObj.PipelineSpec()
	return &pipelineSpec, nil
}
