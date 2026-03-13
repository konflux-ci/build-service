/*
Copyright 2021-2025 Red Hat, Inc.

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
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	"github.com/konflux-ci/build-service/pkg/common"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
	"github.com/konflux-ci/build-service/pkg/git/gitproviderfactory"
	l "github.com/konflux-ci/build-service/pkg/logs"
)

const (
	PipelineRunOnPRExpirationEnvVar   = "IMAGE_TAG_ON_PR_EXPIRATION"
	PipelineRunOnPRExpirationDefault  = "5d"
	pipelineRunOnPushSuffix           = "-on-push"
	pipelineRunOnPRSuffix             = "-on-pull-request"
	pipelineRunOnPushFilename         = "push.yaml"
	pipelineRunOnPRFilename           = "pull-request.yaml"
	pipelinesAsCodeNamespace          = "openshift-pipelines"
	pipelinesAsCodeNamespaceFallback  = "pipelines-as-code"
	pipelinesAsCodeRouteName          = "pipelines-as-code-controller"
	pipelinesAsCodeRouteEnvVar        = "PAC_WEBHOOK_URL"
	pipelinesAsCodeWebhooksSecretName = "pipelines-as-code-webhooks-secret"

	pacCelExpressionAnnotationName = "pipelinesascode.tekton.dev/on-cel-expression"
	pacIncomingSecretNameSuffix    = "-incoming"
	pacIncomingSecretKey           = "incoming-secret"

	pacMergeRequestSourceBranchPrefix = "konflux-"

	appstudioWorkspaceNameLabel      = "appstudio.redhat.com/workspace_name"
	pacCustomParamAppstudioWorkspace = "appstudio_workspace"

	mergeRequestDescription = `
# Pipelines as Code configuration proposal

To start the PipelineRun, add a new comment with content ` + "`/ok-to-test`" + `

For more detailed information about running a PipelineRun, please refer to Pipelines as Code documentation [Running the PipelineRun](https://pipelinesascode.com/docs/guide/running/)

To customize the proposed PipelineRuns after merge, please refer to [Build Pipeline customization](https://konflux-ci.dev/docs/building/customizing-the-build/)

Please follow the block sequence indentation style introduced by the proprosed PipelineRuns YAMLs, or keep using consistent indentation level through your customized PipelineRuns. When different levels are mixed, it will be changed to the proposed style.
`

	// Annotation that specifies git provider id for self hosted SCM instances, e.g. github or gitlab.
	GitProviderAnnotationName = "git-provider"
	// Annotation that specifies git provider API URL.
	// Just git provider URL works in some cases.
	// https://pipelinesascode.com/docs/install/gitlab/#notes
	GitProviderAnnotationURL = "git-provider-url"
)

// GetHttpClientFunction can be mocked in tests.
var GetHttpClientFunction = getHttpClient

// GetWebhookDataAndPacSecret gets and validates webhookSecretString, webhookTargetUrl, pacSecret
// TODO remove newModel handling after only new model is used
func (r *ComponentBuildReconciler) GetWebhookDataAndPacSecret(ctx context.Context, component *compapiv1alpha1.Component) (string, string, *corev1.Secret, error) {
	log := ctrllog.FromContext(ctx).WithName("GetWebhookAndPacSecret")
	newModel := true

	log.Info("Getting webhook and pacSecret")

	gitProvider, err := getGitProvider(*component, newModel)
	if err != nil {
		// Do not reconcile, because configuration must be fixed before it is possible to proceed.
		return "", "", nil, err
	}
	repoUrl := getGitRepoUrl(*component, newModel)

	if strings.HasPrefix(repoUrl, "http:") {
		return "", "", nil, boerrors.NewBuildOpError(boerrors.EHttpUsedForRepository,
			fmt.Errorf("git repository URL can't use insecure HTTP: %s", repoUrl))
	}

	if url, ok := component.Annotations[GitProviderAnnotationURL]; ok {
		if strings.HasPrefix(url, "http:") {
			return "", "", nil, boerrors.NewBuildOpError(boerrors.EHttpUsedForRepository,
				fmt.Errorf("git repository URL in annotation %s can't use insecure HTTP: %s", GitProviderAnnotationURL, repoUrl))
		}
	}

	pacSecret, err := r.lookupPaCSecret(ctx, component, gitProvider, newModel)
	if err != nil {
		return "", "", nil, err
	}

	if err := validatePaCConfiguration(gitProvider, *pacSecret); err != nil {
		r.EventRecorder.Event(pacSecret, "Warning", "ErrorValidatingPaCSecret", err.Error())
		// Do not reconcile, because configuration must be fixed before it is possible to proceed.
		return "", "", nil, boerrors.NewBuildOpError(boerrors.EPaCSecretInvalid,
			fmt.Errorf("invalid configuration in Pipelines as Code secret: %w", err))
	}

	var webhookSecretString string
	if !common.IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
		// Generate webhook secret for the component git repository if not yet generated
		// and stores it in the corresponding k8s secret.
		webhookSecretString, err = r.ensureWebhookSecret(ctx, component, newModel)
		if err != nil {
			return "", "", nil, err
		}
	}

	// Obtain Pipelines as Code callback URL (needed for both webhook mode and app mode for incoming triggers)
	webhookTargetUrl, err := r.getPaCWebhookTargetUrl(ctx, repoUrl, true)
	if err != nil {
		return "", "", nil, err
	}

	return webhookSecretString, webhookTargetUrl, pacSecret, nil
}

// ProvisionPaCForComponentOldModel does Pipelines as Code provision for the given component.
// Mainly, it creates PaC configuration merge request into the component source repositotiry.
// If GitHub PaC application is not configured, creates a webhook for PaC.
// TODO remove after only new model is used
func (r *ComponentBuildReconciler) ProvisionPaCForComponentOldModel(ctx context.Context, component *compapiv1alpha1.Component) (string, error) {
	log := ctrllog.FromContext(ctx).WithName("PaC-setup")
	newModel := false

	log.Info("Starting Pipelines as Code provision for the Component")

	gitProvider, err := getGitProvider(*component, newModel)
	if err != nil {
		// Do not reconcile, because configuration must be fixed before it is possible to proceed.
		return "", err
	}
	repoUrl := getGitRepoUrl(*component, newModel)

	if strings.HasPrefix(repoUrl, "http:") {
		return "", boerrors.NewBuildOpError(boerrors.EHttpUsedForRepository,
			fmt.Errorf("git repository URL can't use insecure HTTP: %s", repoUrl))
	}

	if url, ok := component.Annotations[GitProviderAnnotationURL]; ok {
		if strings.HasPrefix(url, "http:") {
			return "", boerrors.NewBuildOpError(boerrors.EHttpUsedForRepository,
				fmt.Errorf("git repository URL in annotation %s can't use insecure HTTP: %s", GitProviderAnnotationURL, repoUrl))
		}
	}

	pacSecret, err := r.lookupPaCSecret(ctx, component, gitProvider, newModel)
	if err != nil {
		return "", err
	}

	if err := validatePaCConfiguration(gitProvider, *pacSecret); err != nil {
		r.EventRecorder.Event(pacSecret, "Warning", "ErrorValidatingPaCSecret", err.Error())
		// Do not reconcile, because configuration must be fixed before it is possible to proceed.
		return "", boerrors.NewBuildOpError(boerrors.EPaCSecretInvalid,
			fmt.Errorf("invalid configuration in Pipelines as Code secret: %w", err))
	}

	var webhookSecretString, webhookTargetUrl string
	if !common.IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
		// Generate webhook secret for the component git repository if not yet generated
		// and stores it in the corresponding k8s secret.
		webhookSecretString, err = r.ensureWebhookSecret(ctx, component, newModel)
		if err != nil {
			return "", err
		}

		// Obtain Pipelines as Code callback URL
		webhookTargetUrl, err = r.getPaCWebhookTargetUrl(ctx, repoUrl, true)
		if err != nil {
			return "", err
		}
	}

	var pacRepositoryName string
	if pacRepositoryName, err = r.ensurePaCRepository(ctx, component, pacSecret, nil, newModel); err != nil {
		return "", err
	}
	log.Info("Using PaC repository", "PaCRepositoryName", pacRepositoryName, l.Action, l.ActionView)

	// Manage merge request for Pipelines as Code configuration
	mrUrl, err := r.ConfigureRepositoryForPaCOldModel(ctx, component, pacSecret.Data, webhookTargetUrl, webhookSecretString)
	if err != nil {
		r.EventRecorder.Event(component, "Warning", "ErrorConfiguringPaCForComponentRepository", err.Error())
		return "", err
	}
	var mrMessage string
	if mrUrl != "" {
		mrMessage = fmt.Sprintf("Pipelines as Code configuration merge request: %s", mrUrl)
	} else {
		mrMessage = "Pipelines as Code configuration is up to date"
	}
	log.Info(mrMessage)
	r.EventRecorder.Event(component, "Normal", "PipelinesAsCodeConfiguration", mrMessage)

	return mrUrl, nil
}

func getHttpClient() *http.Client { // #nosec G402 // dev instances need insecure, because they have self signed certificates
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: gp.IsInsecureSSL(),
			MinVersion:         tls.VersionTLS13,
		},
	}
	client := &http.Client{Transport: tr}
	return client
}

// validatePaCConfiguration detects checks that all required fields is set for whatever method is used.
func validatePaCConfiguration(gitProvider string, pacSecret corev1.Secret) error {
	if common.IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
		if gitProvider == "github" {
			// GitHub application
			err := checkMandatoryFieldsNotEmpty(pacSecret.Data, []string{common.PipelinesAsCodeGithubAppIdKey, common.PipelinesAsCodeGithubPrivateKey})
			if err != nil {
				return err
			}

			// validate content of the fields
			if _, e := strconv.ParseInt(string(pacSecret.Data[common.PipelinesAsCodeGithubAppIdKey]), 10, 64); e != nil {
				return fmt.Errorf(" Pipelines as Code: failed to parse GitHub application ID. Cause: %w", e)
			}

			privateKey := strings.TrimSpace(string(pacSecret.Data[common.PipelinesAsCodeGithubPrivateKey]))
			if !strings.HasPrefix(privateKey, "-----BEGIN RSA PRIVATE KEY-----") || // notsecret
				!strings.HasSuffix(privateKey, "-----END RSA PRIVATE KEY-----") {
				return fmt.Errorf(" Pipelines as Code secret: GitHub application private key is invalid")
			}
			return nil
		}
		return fmt.Errorf("there is no applications for %s", gitProvider)
	}

	switch pacSecret.Type {
	case corev1.SecretTypeSSHAuth:
		return checkMandatoryFieldsNotEmpty(pacSecret.Data, []string{"ssh-privatekey"})
	case corev1.SecretTypeBasicAuth, corev1.SecretTypeOpaque:
		return checkMandatoryFieldsNotEmpty(pacSecret.Data, []string{"password"})
	default:
		return fmt.Errorf("git secret: unsupported secret type: %s", pacSecret.Type)
	}
}

func checkMandatoryFieldsNotEmpty(config map[string][]byte, mandatoryFields []string) error {
	for _, field := range mandatoryFields {
		if len(config[field]) == 0 {
			return fmt.Errorf("git secret: %s field is not configured", field)
		}
	}
	return nil
}

// TriggerPaCBuildPreparation prepares for triggering builds by ensuring incoming secret and updating incomings for all versions.
// This should be called once before triggering multiple builds to avoid multiple reconciles.
// Returns reconcileRequired flag, incomingSecret, repository, and error.
// TODO remove newModel handling after only new model is used
func (r *ComponentBuildReconciler) TriggerPaCBuildPreparation(
	ctx context.Context,
	component *compapiv1alpha1.Component,
	targetBranches []string,
) (reconcileRequired bool, incomingSecret *corev1.Secret, repository *pacv1alpha1.Repository, err error) {
	log := ctrllog.FromContext(ctx).WithName("TriggerPaCBuildPrep")
	newModel := true

	repository, err = r.findPaCRepositoryForComponent(ctx, component, newModel)
	if err != nil {
		return false, nil, nil, err
	}

	if repository == nil {
		return false, nil, nil, fmt.Errorf("PaC repository not found for component %s", component.Name)
	}

	incomingSecret, reconcileRequired, err = r.ensureIncomingSecret(ctx, component, newModel)
	if err != nil {
		return false, nil, nil, err
	}

	// Update incoming for all target branches
	incomingUpdated := false
	for _, targetBranch := range targetBranches {
		if updateIncoming(repository, incomingSecret.Name, pacIncomingSecretKey, targetBranch) {
			incomingUpdated = true
		}
	}

	if incomingUpdated {
		if err := r.Client.Update(ctx, repository); err != nil {
			log.Error(err, "failed to update PaC repository with incomings", "PaCRepositoryName", repository.Name)
			return false, nil, nil, err
		}
		log.Info("Added incomings to the PaC repository", "PaCRepositoryName", repository.Name, l.Action, l.ActionUpdate)
		reconcileRequired = true
	}

	return reconcileRequired, incomingSecret, repository, nil
}

// TriggerPaCBuild triggers a PaC build using pre-prepared resources from TriggerPaCBuildPrep.
// This method doesn't perform reconciliation and should be called after TriggerPaCBuildPrep.
// TODO remove newModel handling after only new model is used
func (r *ComponentBuildReconciler) TriggerPaCBuild(
	ctx context.Context,
	component *compapiv1alpha1.Component,
	sanitizedVersionName string,
	targetBranch string,
	webhookTargetUrl string,
	incomingSecret *corev1.Secret,
	repository *pacv1alpha1.Repository,
) error {
	log := ctrllog.FromContext(ctx).WithName("TriggerPaCBuild")
	newModel := true

	repoUrl := getGitRepoUrl(*component, newModel)
	secretValue := string(incomingSecret.Data[pacIncomingSecretKey])
	pipelineRunName := getPipelineRunDefinitionName(component.Name, sanitizedVersionName, false)

	triggerURL := fmt.Sprintf("%s/incoming", webhookTargetUrl)
	HttpClient := GetHttpClientFunction()

	// we have to supply source_url as additional param, because PaC isn't able to resolve it for trigger
	jsonTemplate := `{"params": {"source_url": "%s"}, "secret": "%s", "repository": "%s", "branch": "%s", "pipelinerun": "%s", "namespace": "%s"}`
	bytesParam := []byte(fmt.Sprintf(jsonTemplate, repoUrl, secretValue, repository.Name, targetBranch, pipelineRunName, repository.Namespace))

	resp, err := HttpClient.Post(triggerURL, "application/json", bytes.NewBuffer(bytesParam))

	if err != nil {
		log.Error(err, "error from incoming webhook trigger POST")
		return nil
	}

	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		// ignore 503 and 504 for now, until PAC fixes issue https://issues.redhat.com/browse/SRVKP-4352
		log.Info(fmt.Sprintf("PaC incoming endpoint %s with params %s returned HTTP %d", triggerURL, string(bytesParam), resp.StatusCode))
		if resp.StatusCode == 503 || resp.StatusCode == 504 {
			return nil
		}
		return fmt.Errorf("PaC incoming endpoint %s with params %s returned HTTP %d", triggerURL, string(bytesParam), resp.StatusCode)
	}

	log.Info(fmt.Sprintf("PaC build manually triggered push pipeline for component: %s, version: %s, endpoint %s with params %s", component.Name, sanitizedVersionName, triggerURL, string(bytesParam)))
	return nil
}

// TriggerPaCBuildOldModel triggers PaC builds for the old model.
// For new model, use TriggerPaCBuildPrep followed by TriggerPaCBuild.
// TODO remove after only new model is used
func (r *ComponentBuildReconciler) TriggerPaCBuildOldModel(ctx context.Context, component *compapiv1alpha1.Component) (bool, error) {
	log := ctrllog.FromContext(ctx).WithName("TriggerPaCBuild")
	newModel := false

	repository, err := r.findPaCRepositoryForComponent(ctx, component, newModel)
	if err != nil {
		return false, err
	}

	if repository == nil {
		return false, fmt.Errorf("PaC repository not found for component %s", component.Name)
	}

	incomingSecret, reconcileRequired, err := r.ensureIncomingSecret(ctx, component, newModel)
	if err != nil {
		return false, err
	}

	repoUrl := getGitRepoUrl(*component, newModel)
	gitProvider, err := getGitProvider(*component, newModel)
	if err != nil {
		// There is no point to continue if git provider is not known.
		return false, err
	}

	pacSecret, err := r.lookupPaCSecret(ctx, component, gitProvider, newModel)
	if err != nil {
		return false, err
	}

	gitClient, err := gitproviderfactory.CreateGitClient(gitproviderfactory.GitClientConfig{
		PacSecretData: pacSecret.Data,
		GitProvider:   gitProvider,
		RepoUrl:       repoUrl,
	})
	if err != nil {
		return false, err
	}

	// getting branch in advance just to test credentials
	defaultBranch, err := gitClient.GetDefaultBranchWithChecks(repoUrl)
	if err != nil {
		return false, err
	}

	// get target branch for incoming hook
	targetBranch := component.Spec.Source.GitSource.Revision
	if targetBranch == "" {
		targetBranch = defaultBranch
	}

	incomingUpdated := updateIncoming(repository, incomingSecret.Name, pacIncomingSecretKey, targetBranch)
	if incomingUpdated {
		if err := r.Client.Update(ctx, repository); err != nil {
			log.Error(err, "failed to update PaC repository with incomings", "PaCRepositoryName", repository.Name)
			return false, err
		}
		log.Info("Added incomings to the PaC repository", "PaCRepositoryName", repository.Name, l.Action, l.ActionUpdate)

		// reconcile to be sure that Repository is updated, as Repository needs to have correct incomings for trigger to work
		return true, nil
	}

	// reconcile to be sure that Secret is created
	if reconcileRequired {
		return true, nil
	}

	webhookTargetUrl, err := r.getPaCWebhookTargetUrl(ctx, repoUrl, false)
	if err != nil {
		return false, err
	}

	secretValue := string(incomingSecret.Data[pacIncomingSecretKey])

	pipelineRunName := getPipelineRunDefinitionName(component.Name, "", false)

	triggerURL := fmt.Sprintf("%s/incoming", webhookTargetUrl)
	HttpClient := GetHttpClientFunction()

	// we have to supply source_url as additional param, because PaC isn't able to resolve it for trigger
	jsonTemplate := `{"params": {"source_url": "%s"}, "secret": "%s", "repository": "%s", "branch": "%s", "pipelinerun": "%s", "namespace": "%s"}`
	bytesParam := []byte(fmt.Sprintf(jsonTemplate, repoUrl, secretValue, repository.Name, targetBranch, pipelineRunName, repository.Namespace))

	resp, err := HttpClient.Post(triggerURL, "application/json", bytes.NewBuffer(bytesParam))

	if err != nil {
		log.Error(err, "error from incoming webhook trigger POST")
		return false, nil
	}

	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		// ignore 503 and 504 for now, until PAC fixes issue https://issues.redhat.com/browse/SRVKP-4352
		log.Info(fmt.Sprintf("PaC incoming endpoint %s with params %s returned HTTP %d", triggerURL, string(bytesParam), resp.StatusCode))
		if resp.StatusCode == 503 || resp.StatusCode == 504 {
			return false, nil
		}
		return false, fmt.Errorf("PaC incoming endpoint %s with params %s returned HTTP %d", triggerURL, string(bytesParam), resp.StatusCode)
	}

	log.Info(fmt.Sprintf("PaC build manually triggered push pipeline for component: %s, endpoint %s with params %s", component.Name, triggerURL, string(bytesParam)))
	return false, nil
}

// UndoPaCProvisionForComponentOldModel creates merge request that removes Pipelines as Code configuration from component source repository.
// Deletes PaC webhook if used.
// In case of any errors just logs them and does not block Component deletion.
// TODO remove after only new model is used
func (r *ComponentBuildReconciler) UndoPaCProvisionForComponentOldModel(ctx context.Context, component *compapiv1alpha1.Component) (string, error) {
	log := ctrllog.FromContext(ctx).WithName("PaC-cleanup")
	newModel := false

	log.Info("Starting Pipelines as Code unprovision for the Component")

	gitProvider, err := getGitProvider(*component, newModel)
	if err != nil {
		// There is no point to continue if git provider is not known.
		return "", err
	}

	pacSecret, err := r.lookupPaCSecret(ctx, component, gitProvider, newModel)
	if err != nil {
		log.Error(err, "error getting git provider credentials secret", l.Action, l.ActionView)
		// Cannot continue without accessing git provider credentials.
		return "", boerrors.NewBuildOpError(boerrors.EPaCSecretNotFound, err)
	}

	repoUrl := getGitRepoUrl(*component, newModel)
	webhookTargetUrl := ""
	if !common.IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
		webhookTargetUrl, err = r.getPaCWebhookTargetUrl(ctx, repoUrl, true)
		if err != nil {
			// Just log the error and continue with pruning merge request creation
			log.Error(err, "failed to get Pipelines as Code webhook target URL. Webhook will not be deleted.", l.Action, l.ActionView, l.Audit, "true")
		}
	}

	// Manage merge request for Pipelines as Code configuration removal
	baseBranch, mrUrl, action, err := r.UnconfigureRepositoryForPacOldModel(ctx, component, pacSecret.Data, webhookTargetUrl)
	if err != nil {
		log.Error(err, "failed to create merge request to remove Pipelines as Code configuration from Component source repository", l.Audit, "true")
		return "", err
	}

	err = r.cleanupPaCRepositoryIncomingsAndSecretOldModel(ctx, component, baseBranch, newModel)
	if err != nil {
		log.Error(err, "failed cleanup incomings from repo and incoming secret")
		return "", err
	}

	switch action {
	case "delete":
		if mrUrl != "" {
			log.Info(fmt.Sprintf("Pipelines as Code configuration removal merge request: %s", mrUrl))
		} else {
			log.Info("Pipelines as Code configuration removal merge request is not needed")
		}
	case "close":
		log.Info(fmt.Sprintf("Pipelines as Code configuration merge request has been closed: %s", mrUrl))
	}
	return mrUrl, nil
}

// getPaCWebhookTargetUrl returns URL to which events from git repository should be sent.
// it will first try to get url from env variable
// then when useWebhookUrlConfig is true it will try to get it from webhook config
// and lastly it will try to get it from PaC route url
func (r *ComponentBuildReconciler) getPaCWebhookTargetUrl(ctx context.Context, repositoryURL string, useWebhookUrlConfig bool) (string, error) {
	webhookTargetUrl := os.Getenv(pipelinesAsCodeRouteEnvVar)

	if webhookTargetUrl == "" && useWebhookUrlConfig {
		webhookTargetUrl = r.WebhookURLLoader.Load(repositoryURL)
	}

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
		if !errors.IsNotFound(err) {
			return "", fmt.Errorf("failed to get Pipelines as Code route in %s namespace: %w", pacWebhookRouteKey.Namespace, err)
		}
		// Fallback to old PaC namesapce
		pacWebhookRouteKey.Namespace = pipelinesAsCodeNamespaceFallback
		if err := r.Client.Get(ctx, pacWebhookRouteKey, pacWebhookRoute); err != nil {
			if !errors.IsNotFound(err) {
				return "", fmt.Errorf("failed to get Pipelines as Code route in %s namespace: %w", pacWebhookRouteKey.Namespace, err)
			}
			// Pipelines as Code public route was not found in expected namespaces
			// Consider this error permanent
			return "", boerrors.NewBuildOpError(boerrors.EPaCRouteDoesNotExist,
				fmt.Errorf("PaC route not found in %s nor %s namespace", pipelinesAsCodeNamespace, pipelinesAsCodeNamespaceFallback))
		}
	}
	return "https://" + pacWebhookRoute.Spec.Host, nil
}

// TODO remove newModel handling after only new model is used
func generateMergeRequestSourceBranch(component *compapiv1alpha1.Component, version string) string {
	if version != "" {
		return fmt.Sprintf("%s%s-%s", pacMergeRequestSourceBranchPrefix, component.Name, version)
	} else {
		return fmt.Sprintf("%s%s", pacMergeRequestSourceBranchPrefix, component.Name)
	}
}

// TODO remove newModel handling after only new model is used
// getPipelineRunDefinitionFilePath returns full path in git repository to the pipeline run definition of the given Component.
func getPipelineRunDefinitionFilePath(componentName, versionName string, isPullRequest bool) string {
	pipelineNameSuffix := pipelineRunOnPushFilename
	if isPullRequest {
		pipelineNameSuffix = pipelineRunOnPRFilename
	}
	if versionName != "" {
		return ".tekton/" + componentName + "-" + versionName + "-" + pipelineNameSuffix
	} else {
		return ".tekton/" + componentName + "-" + pipelineNameSuffix
	}
}

// GetGitClient creates and returns a git provider client for the component's git repository.
// It uses the PaC configuration to authenticate with the git provider (GitHub, GitLab, etc.).
// TODO remove newModel handling after only new model is used
func GetGitClient(component *compapiv1alpha1.Component, pacConfig map[string][]byte) (gp.GitProviderClient, error) {
	newModel := true
	gitProvider, _ := getGitProvider(*component, newModel)
	repoUrl := getGitRepoUrl(*component, newModel)

	gitClient, err := gitproviderfactory.CreateGitClient(gitproviderfactory.GitClientConfig{
		PacSecretData: pacConfig,
		GitProvider:   gitProvider,
		RepoUrl:       repoUrl,
	})
	if err != nil {
		return nil, err
	}

	// getting branch just to test credentials
	_, err = gitClient.GetDefaultBranchWithChecks(repoUrl)
	if err != nil {
		return nil, err
	}

	return gitClient, nil
}

// SetupPacWebhookWhenAppNotUsed configures a webhook in the component's git repository to notify the in-cluster PaC controller.
// The webhook is only created when PaC GitHub/GitLab App is not configured (checks via IsPaCApplicationConfigured).
// When using App-based integration, webhooks are managed automatically by the git provider and this setup is skipped.
// TODO remove newModel handling after only new model is used
func (r *ComponentBuildReconciler) SetupPacWebhookWhenAppNotUsed(ctx context.Context, component *compapiv1alpha1.Component, gitClient gp.GitProviderClient, pacConfig map[string][]byte, webhookTargetUrl, webhookSecret string) error {
	log := ctrllog.FromContext(ctx).WithValues("repository", component.Spec.Source.GitURL)
	newModel := true

	gitProvider, _ := getGitProvider(*component, newModel)
	repoUrl := getGitRepoUrl(*component, newModel)

	isAppUsed := common.IsPaCApplicationConfigured(gitProvider, pacConfig)
	if !isAppUsed {
		if err := gitClient.SetupPaCWebhook(repoUrl, webhookTargetUrl, webhookSecret); err != nil {
			log.Error(err, fmt.Sprintf("failed to setup Pipelines as Code webhook %s for %s Component in %s namespace", webhookTargetUrl, component.Name, component.Namespace), l.Audit, "true")
			return err
		} else {
			log.Info(fmt.Sprintf("Pipelines as Code webhook \"%s\" configured for %s Component in %s namespace",
				webhookTargetUrl, component.Name, component.Namespace),
				l.Audit, "true")
		}
	}
	return nil
}

// CreatePipelineRunsInRepository creates a merge request with pipeline run definitions in the component's git repository.
// Returns the merge request URL if created, or empty string if pipeline runs are already up to date.
// TODO remove newModel handling after only new model is used
func (r *ComponentBuildReconciler) CreatePipelineRunsInRepository(ctx context.Context, component *compapiv1alpha1.Component, gitClient gp.GitProviderClient, versionInfo *VersionInfo, pipelineDefinition *VersionPipelineDefinition, pacConfig map[string][]byte) (prUrl string, err error) {
	log := ctrllog.FromContext(ctx).WithValues("repository", component.Spec.Source.GitURL)
	ctx = ctrllog.IntoContext(ctx, log)
	newModel := true

	pipelineRunOnPushYaml, pipelineRunOnPRYaml, err := r.generatePaCPipelineRunConfigs(ctx, component, gitClient, versionInfo, pipelineDefinition)
	if err != nil {
		return "", err
	}

	gitProvider, _ := getGitProvider(*component, newModel)
	authorName := "konflux"
	namePrefix := "Konflux"

	// Check if app is used and override defaults with app-specific values
	isAppUsed := common.IsPaCApplicationConfigured(gitProvider, pacConfig)
	if isAppUsed {
		// Customize PR data to reflect git application name
		if appName, appSlug, err := gitClient.GetConfiguredGitAppName(); err == nil {
			namePrefix = appName
			authorName = appSlug
		} else {
			if gitProvider == "github" {
				log.Error(err, "failed to get PaC GitHub Application name", l.Action, l.ActionView, l.Audit, "true")
				// Do not fail if failed to read GitHub App info
			}
		}
	}

	mrData := &gp.MergeRequestData{
		CommitMessage:  fmt.Sprintf("%s update %s:%s", namePrefix, component.Name, versionInfo.OriginalVersion),
		SignedOff:      true,
		BranchName:     generateMergeRequestSourceBranch(component, versionInfo.SanitizedVersion),
		BaseBranchName: versionInfo.Revision,
		Title:          fmt.Sprintf("%s update %s:%s", namePrefix, component.Name, versionInfo.OriginalVersion),
		Text:           mergeRequestDescription,
		AuthorName:     authorName,
		AuthorEmail:    "konflux@no-reply.konflux-ci.dev",
		Files: []gp.RepositoryFile{
			{FullPath: getPipelineRunDefinitionFilePath(component.Name, versionInfo.SanitizedVersion, false), Content: pipelineRunOnPushYaml},
			{FullPath: getPipelineRunDefinitionFilePath(component.Name, versionInfo.SanitizedVersion, true), Content: pipelineRunOnPRYaml},
		},
	}

	repoUrl := getGitRepoUrl(*component, newModel)
	mrUrl, err := gitClient.EnsurePaCMergeRequest(repoUrl, mrData)
	if err != nil {
		return "", err
	}

	var mrMessage string
	if mrUrl != "" {
		mrMessage = fmt.Sprintf("Pipelines as Code configuration merge request: %s, for component: %s, version: %s", mrUrl, component.Name, versionInfo.SanitizedVersion)
	} else {
		mrMessage = fmt.Sprintf("Pipelines as Code configuration is up to date for component: %s, version: %s", component.Name, versionInfo.SanitizedVersion)
	}
	log.Info(mrMessage)
	r.EventRecorder.Event(component, "Normal", "PipelinesAsCodeConfiguration", mrMessage)
	return mrUrl, nil
}

// ConfigureRepositoryForPaCOldModel creates a merge request with initial Pipelines as Code configuration
// and configures a webhook to notify in-cluster PaC unless application (on the repository side) is used.
// TODO remove after only new model is used
func (r *ComponentBuildReconciler) ConfigureRepositoryForPaCOldModel(ctx context.Context, component *compapiv1alpha1.Component, pacConfig map[string][]byte, webhookTargetUrl, webhookSecret string) (prUrl string, err error) {
	log := ctrllog.FromContext(ctx).WithValues("repository", component.Spec.Source.GitSource.URL)
	newModel := false

	gitProvider, _ := getGitProvider(*component, newModel)
	repoUrl := getGitRepoUrl(*component, newModel)

	gitClient, err := gitproviderfactory.CreateGitClient(gitproviderfactory.GitClientConfig{
		PacSecretData: pacConfig,
		GitProvider:   gitProvider,
		RepoUrl:       repoUrl,
	})
	if err != nil {
		return "", err
	}

	// getting branch in advance just to test credentials
	defaultBranch, err := gitClient.GetDefaultBranchWithChecks(repoUrl)
	if err != nil {
		return "", err
	}

	baseBranch := component.Spec.Source.GitSource.Revision
	if baseBranch == "" {
		baseBranch = defaultBranch
	}

	pipelineRunOnPushYaml, pipelineRunOnPRYaml, err := r.generatePaCPipelineRunConfigsOldModel(ctx, component, gitClient, baseBranch, newModel)
	if err != nil {
		return "", err
	}

	mrData := &gp.MergeRequestData{
		CommitMessage:  "Konflux update " + component.Name,
		SignedOff:      true,
		BranchName:     generateMergeRequestSourceBranch(component, ""),
		BaseBranchName: baseBranch,
		Title:          "Konflux update " + component.Name,
		Text:           mergeRequestDescription,
		AuthorName:     "konflux",
		AuthorEmail:    "konflux@no-reply.konflux-ci.dev",
		Files: []gp.RepositoryFile{
			{FullPath: getPipelineRunDefinitionFilePath(component.Name, "", false), Content: pipelineRunOnPushYaml},
			{FullPath: getPipelineRunDefinitionFilePath(component.Name, "", true), Content: pipelineRunOnPRYaml},
		},
	}

	isAppUsed := common.IsPaCApplicationConfigured(gitProvider, pacConfig)
	if isAppUsed {
		// Customize PR data to reflect git application name
		if appName, appSlug, err := gitClient.GetConfiguredGitAppName(); err == nil {
			mrData.CommitMessage = fmt.Sprintf("%s update %s", appName, component.Name)
			mrData.Title = fmt.Sprintf("%s update %s", appName, component.Name)
			mrData.AuthorName = appSlug
		} else {
			if gitProvider == "github" {
				log.Error(err, "failed to get PaC GitHub Application name", l.Action, l.ActionView, l.Audit, "true")
				// Do not fail PaC provision if failed to read GitHub App info
			}
		}
	} else {
		// Webhook
		if err := gitClient.SetupPaCWebhook(repoUrl, webhookTargetUrl, webhookSecret); err != nil {
			log.Error(err, fmt.Sprintf("failed to setup Pipelines as Code webhook %s", webhookTargetUrl), l.Audit, "true")
			return "", err
		} else {
			log.Info(fmt.Sprintf("Pipelines as Code webhook \"%s\" configured for %s Component in %s namespace",
				webhookTargetUrl, component.Name, component.Namespace),
				l.Audit, "true")
		}
	}

	// It might seem that there is more optimal way of doing this.
	// However, this use case is not often used, so making logic above more complicated does not worth it.
	if component.Annotations[BuildRequestAnnotationName] == BuildRequestConfigurePaCNoMrAnnotationValue {
		// User requested not to create a proposal PR.
		return "", nil
	}
	return gitClient.EnsurePaCMergeRequest(repoUrl, mrData)
}

// RemovePacWebhook removes repository webhook, but only when no other component is using it, used only when component is removed
// TODO remove newModel handling after only new model is used
func (r *ComponentBuildReconciler) RemovePacWebhook(ctx context.Context, component *compapiv1alpha1.Component, gitClient gp.GitProviderClient, pacConfig map[string][]byte, webhookTargetUrl string) error {
	log := ctrllog.FromContext(ctx)
	newModel := true

	gitProvider, _ := getGitProvider(*component, newModel)
	repoUrl := getGitRepoUrl(*component, newModel)

	isAppUsed := common.IsPaCApplicationConfigured(gitProvider, pacConfig)
	if !isAppUsed && webhookTargetUrl != "" {
		componentList := &compapiv1alpha1.ComponentList{}
		if err := r.Client.List(ctx, componentList, &client.ListOptions{Namespace: component.Namespace}); err != nil {
			log.Error(err, "failed to list components")
			return err
		}

		sameRepoUsed := false
		for _, comp := range componentList.Items {
			if comp.Name == component.Name {
				continue
			}
			componentUrl := getGitRepoUrl(comp, newModel)
			if componentUrl == repoUrl {
				sameRepoUsed = true
				break
			}
		}

		if !sameRepoUsed {
			err := gitClient.DeletePaCWebhook(repoUrl, webhookTargetUrl)
			if err != nil {
				// Just log the error
				log.Error(err, fmt.Sprintf("failed to delete Pipelines as Code webhook %s, for %s Component in %s namespace", webhookTargetUrl, component.Name, component.Namespace), l.Action, l.ActionDelete, l.Audit, "true")
			} else {
				log.Info(fmt.Sprintf("Pipelines as Code webhook \"%s\" deleted for %s Component in %s namespace",
					webhookTargetUrl, component.Name, component.Namespace),
					l.Action, l.ActionDelete)
			}
		}
	}

	return nil
}

// RemovePipelineRunsFromRepository removes pipeline run definitions from component's repository.
// It first closes any unmerged MRs by deleting their source branches.
// If the pipeline runs were already merged, it creates a new merge request to delete the pipeline run files.
// Does not delete the GitHub application from the repository as its installation was done manually by the user.
// TODO remove newModel handling after only new model is used
func (r *ComponentBuildReconciler) RemovePipelineRunsFromRepository(ctx context.Context, component *compapiv1alpha1.Component, versionInfo *VersionInfo, gitClient gp.GitProviderClient, pacConfig map[string][]byte) error {
	log := ctrllog.FromContext(ctx)
	newModel := true

	log.Info("Starting Pipelines as Code unprovision for the Component", "componentName", component.Name, "versionName", versionInfo.OriginalVersion)

	gitProvider, _ := getGitProvider(*component, newModel)
	repoUrl := getGitRepoUrl(*component, newModel)

	sourceBranch := generateMergeRequestSourceBranch(component, versionInfo.SanitizedVersion)

	authorName := "konflux"
	namePrefix := "Konflux"

	// Check if app is used and override defaults with app-specific values
	isAppUsed := common.IsPaCApplicationConfigured(gitProvider, pacConfig)
	if isAppUsed {
		// Customize PR data to reflect git application name
		if appName, appSlug, err := gitClient.GetConfiguredGitAppName(); err == nil {
			namePrefix = appName
			authorName = appSlug
		} else {
			if gitProvider == "github" {
				log.Error(err, "failed to get PaC GitHub Application name", l.Action, l.ActionView, l.Audit, "true")
				// Do not fail PaC clean up PR if failed to read GitHub App info
			}
		}
	}

	mrData := &gp.MergeRequestData{
		BranchName:     sourceBranch,
		BaseBranchName: versionInfo.Revision,
		AuthorName:     authorName,
	}

	mergeRequest, err := gitClient.FindUnmergedPaCMergeRequest(repoUrl, mrData)
	if err != nil {
		return err
	}

	// Non-existing source branch should not be an error, just ignore it,
	// but other errors should be handled.
	branchDeleted, err := gitClient.DeleteBranch(repoUrl, sourceBranch)
	if err != nil {
		return err
	}
	if branchDeleted {
		log.Info(fmt.Sprintf("PaC configuration proposal branch %s is deleted, for component %s, version %s", sourceBranch, component.Name, versionInfo.OriginalVersion), l.Action, l.ActionDelete)
	}
	if mergeRequest != nil {
		log.Info(fmt.Sprintf("Pipelines as Code configuration merge request has been closed: %s", mergeRequest.WebUrl))
	}

	// We did close open pull request (by removing source branch),
	// but if component was onboarded before and PR was merged and then re-onboard (just closing re-onboard PR won't remove files)
	pullPipelineRunFileExists, err := gitClient.IsFileExist(repoUrl, versionInfo.Revision, getPipelineRunDefinitionFilePath(component.Name, versionInfo.SanitizedVersion, true))
	if err != nil {
		return fmt.Errorf("failed to check if pipeline run file exists: %w", err)
	}
	pushPipelineRunFileExists, err := gitClient.IsFileExist(repoUrl, versionInfo.Revision, getPipelineRunDefinitionFilePath(component.Name, versionInfo.SanitizedVersion, false))
	if err != nil {
		return fmt.Errorf("failed to check if pipeline file exists: %w", err)
	}

	if pullPipelineRunFileExists || pushPipelineRunFileExists {
		// Configuration PR was already merged
		// Create new PaC configuration clean up merge request

		mrData = &gp.MergeRequestData{
			CommitMessage:  fmt.Sprintf("%s purge %s:%s", namePrefix, component.Name, versionInfo.OriginalVersion),
			SignedOff:      true,
			BranchName:     fmt.Sprintf("konflux-purge-%s-%s", component.Name, versionInfo.SanitizedVersion),
			BaseBranchName: versionInfo.Revision,
			Title:          fmt.Sprintf("%s purge %s:%s", namePrefix, component.Name, versionInfo.OriginalVersion),
			Text:           "Pipelines as Code configuration removal",
			AuthorName:     authorName,
			AuthorEmail:    "konflux@no-reply.konflux-ci.dev",
			Files: []gp.RepositoryFile{
				{FullPath: getPipelineRunDefinitionFilePath(component.Name, versionInfo.SanitizedVersion, false)},
				{FullPath: getPipelineRunDefinitionFilePath(component.Name, versionInfo.SanitizedVersion, true)},
			},
		}

		prUrl, err := gitClient.UndoPaCMergeRequest(repoUrl, mrData)
		if err == nil {
			if prUrl != "" {
				log.Info(fmt.Sprintf("Pipelines as Code configuration removal merge request: %s", prUrl))
			} else {
				log.Info("Pipelines as Code configuration removal merge request is not needed")
			}
		} else {
			log.Error(err, "failed to create merge request to remove Pipelines as Code configuration from Component source repository", "componentName", component.Name, "versionName", versionInfo.OriginalVersion, l.Audit, "true")
		}
		return err
	}
	return nil
}

// UnconfigureRepositoryForPacOldModel creates a merge request that deletes Pipelines as Code configuration of the diven component in its repository.
// Deletes PaC webhook if it's used.
// Does not delete PaC GitHub application from the repository as its installation was done manually by the user.
// Returns merge request web URL or empty string if it's not needed.
// TODO remove after only new model is used
func (r *ComponentBuildReconciler) UnconfigureRepositoryForPacOldModel(ctx context.Context, component *compapiv1alpha1.Component, pacConfig map[string][]byte, webhookTargetUrl string) (baseBranch string, prUrl string, action string, err error) {
	log := ctrllog.FromContext(ctx)
	newModel := false

	gitProvider, _ := getGitProvider(*component, newModel)
	repoUrl := getGitRepoUrl(*component, newModel)

	gitClient, err := gitproviderfactory.CreateGitClient(gitproviderfactory.GitClientConfig{
		PacSecretData: pacConfig,
		GitProvider:   gitProvider,
		RepoUrl:       repoUrl,
	})
	if err != nil {
		return "", "", "", err
	}

	// getting branch in advance just to test credentials
	defaultBranch, err := gitClient.GetDefaultBranchWithChecks(repoUrl)
	if err != nil {
		return "", "", "", err
	}

	isAppUsed := common.IsPaCApplicationConfigured(gitProvider, pacConfig)
	if !isAppUsed && webhookTargetUrl != "" {
		componentList := &compapiv1alpha1.ComponentList{}
		if err := r.Client.List(ctx, componentList, &client.ListOptions{Namespace: component.Namespace}); err != nil {
			log.Error(err, "failed to list components")
			return "", "", "", err
		}

		sameRepoUsed := false
		for _, comp := range componentList.Items {
			if comp.Name == component.Name {
				continue
			}
			componentUrl := getGitRepoUrl(comp, newModel)
			if componentUrl == repoUrl {
				sameRepoUsed = true
				break
			}
		}

		if !sameRepoUsed {
			err = gitClient.DeletePaCWebhook(repoUrl, webhookTargetUrl)
			if err != nil {
				// Just log the error and continue with merge request creation
				log.Error(err, fmt.Sprintf("failed to delete Pipelines as Code webhook %s", webhookTargetUrl), l.Action, l.ActionDelete, l.Audit, "true")
			} else {
				log.Info(fmt.Sprintf("Pipelines as Code webhook \"%s\" deleted for %s Component in %s namespace",
					webhookTargetUrl, component.Name, component.Namespace),
					l.Action, l.ActionDelete)
			}
		}
	}

	sourceBranch := generateMergeRequestSourceBranch(component, "")
	baseBranch = component.Spec.Source.GitSource.Revision
	if baseBranch == "" {
		baseBranch = defaultBranch
	}

	mrData := &gp.MergeRequestData{
		BranchName:     sourceBranch,
		BaseBranchName: baseBranch,
		AuthorName:     "konflux",
	}

	mergeRequest, err := gitClient.FindUnmergedPaCMergeRequest(repoUrl, mrData)
	if err != nil {
		return baseBranch, "", "", err
	}

	action_done := "close"
	// Close merge request.
	// To close a merge request it's enough to delete the branch.

	// Non-existing source branch should not be an error, just ignore it,
	// but other errors should be handled.
	branchDeleted, err := gitClient.DeleteBranch(repoUrl, sourceBranch)
	if err != nil {
		return baseBranch, prUrl, action_done, err
	}
	if branchDeleted {
		log.Info(fmt.Sprintf("PaC configuration proposal branch %s is deleted", sourceBranch), l.Action, l.ActionDelete)
	}

	if mergeRequest == nil {
		// Create new PaC configuration clean up merge request
		mrData = &gp.MergeRequestData{
			CommitMessage:  "Konflux purge " + component.Name,
			SignedOff:      true,
			BranchName:     "konflux-purge-" + component.Name,
			BaseBranchName: baseBranch,
			Title:          "Konflux purge " + component.Name,
			Text:           "Pipelines as Code configuration removal",
			AuthorName:     "konflux",
			AuthorEmail:    "konflux@no-reply.konflux-ci.dev",
			Files: []gp.RepositoryFile{
				{FullPath: getPipelineRunDefinitionFilePath(component.Name, "", false)},
				{FullPath: getPipelineRunDefinitionFilePath(component.Name, "", true)},
			},
		}

		if isAppUsed {
			// Customize PR data to reflect git application name
			if appName, appSlug, err := gitClient.GetConfiguredGitAppName(); err == nil {
				mrData.CommitMessage = fmt.Sprintf("%s purge %s", appName, component.Name)
				mrData.Title = fmt.Sprintf("%s purge %s", appName, component.Name)
				mrData.AuthorName = appSlug
			} else {
				if gitProvider == "github" {
					log.Error(err, "failed to get PaC GitHub Application name", l.Action, l.ActionView, l.Audit, "true")
					// Do not fail PaC clean up PR if failed to read GitHub App info
				}
			}
		}

		action_done = "delete"
		prUrl, err = gitClient.UndoPaCMergeRequest(repoUrl, mrData)
	}

	return baseBranch, prUrl, action_done, err
}

// TODO remove newModel handling after only new model is used
// getGitRepoUrl returns trimmed source url
func getGitRepoUrl(component compapiv1alpha1.Component, newModel bool) string {
	if newModel {
		return strings.TrimSuffix(strings.TrimSuffix(component.Spec.Source.GitURL, "/"), ".git")
	} else {
		return strings.TrimSuffix(strings.TrimSuffix(component.Spec.Source.GitSource.URL, "/"), ".git")
	}
}

// TODO remove newModel handling after only new model is used
// validateGitSourceUrl validates if component.Spec.Source.GitSource.URL is valid git url
// https://github.com/owner/repository is valid
// https://github.com/owner is invalid
func validateGitSourceUrl(component compapiv1alpha1.Component, gitProvider string, newModel bool) error {
	sourceUrl := getGitRepoUrl(component, newModel)
	gitUrl, err := url.Parse(sourceUrl)
	if err != nil {
		return err
	}
	shouldFail := false
	gitSourceUrlPathParts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(gitUrl.Path, "/"), "/"), "/")
	if len(gitSourceUrlPathParts) < 2 {
		shouldFail = true
	}

	if gitProvider == "github" {
		if len(gitSourceUrlPathParts) > 2 {
			shouldFail = true
		}
	}

	if gitProvider == "gitlab" {
		if slices.Contains(gitSourceUrlPathParts, "-") {
			shouldFail = true
		}
	}

	if shouldFail {
		err := fmt.Errorf("git source URL is not valid git URL '%s' for %s Component in %s namespace", sourceUrl, component.Name, component.Namespace)
		return err
	}
	return nil
}

// TODO remove newModel handling after only new model is used
// getGitProvider returns git provider name based on the repository url or the git-provider annotation
func getGitProvider(component compapiv1alpha1.Component, newModel bool) (string, error) {
	allowedGitProviders := []string{"github", "gitlab", "forgejo"}
	if newModel {
		if component.Spec.Source.GitURL == "" {
			return "", boerrors.NewBuildOpError(boerrors.EWrongGitSourceUrl, fmt.Errorf("git source URL is not set for %s Component in %s namespace", component.Name, component.Namespace))
		}
	} else {
		if component.Spec.Source.GitSource == nil {
			return "", boerrors.NewBuildOpError(boerrors.EWrongGitSourceUrl, fmt.Errorf("git source URL is not set for %s Component in %s namespace", component.Name, component.Namespace))
		}
	}

	gitProvider := component.GetAnnotations()[GitProviderAnnotationName]
	if gitProvider != "" && !slices.Contains(allowedGitProviders, gitProvider) {
		return "", boerrors.NewBuildOpError(boerrors.EUnknownGitProvider, fmt.Errorf(`unsupported "%s" annotation value: %s`, GitProviderAnnotationName, gitProvider))
	}

	if gitProvider == "" {
		var sourceUrl string
		if newModel {
			sourceUrl = component.Spec.Source.GitURL
		} else {
			sourceUrl = component.Spec.Source.GitSource.URL
		}
		var host string

		// sourceUrl example: https://github.com/konflux-ci/build-service
		u, err := url.Parse(sourceUrl)
		if err != nil {
			return "", boerrors.NewBuildOpError(boerrors.EWrongGitSourceUrl, err)
		}
		host = u.Hostname()

		for _, provider := range allowedGitProviders {
			if strings.Contains(host, provider) {
				gitProvider = provider
				break
			}
		}
	}

	if gitProvider == "" {
		return "", boerrors.NewBuildOpError(boerrors.EUnknownGitProvider, fmt.Errorf(`failed to determine git provider, please set the "%s" annotation on the component`, GitProviderAnnotationName))
	}

	// validate gitsource URL
	err := validateGitSourceUrl(component, gitProvider, newModel)
	if err != nil {
		return "", boerrors.NewBuildOpError(boerrors.EWrongGitSourceUrl, err)
	}

	return gitProvider, nil
}
