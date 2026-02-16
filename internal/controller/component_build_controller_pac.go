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

	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	. "github.com/konflux-ci/build-service/pkg/common"
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

To customize the proposed PipelineRuns after merge, please refer to [Build Pipeline customization](https://konflux-ci.dev/docs/how-tos/configuring/)

Please follow the block sequence indentation style introduced by the proprosed PipelineRuns YAMLs, or keep using consistent indentation level through your customized PipelineRuns. When different levels are mixed, it will be changed to the proposed style.
`

	// Annotation that specifies git provider id for self hosted SCM instances, e.g. github or gitlab.
	GitProviderAnnotationName = "git-provider"
	// Annotation that specifies git provider API URL.
	// Just git provider URL works in some cases.
	// https://pipelinesascode.com/docs/install/gitlab/#notes
	GitProviderAnnotationURL = "git-provider-url"
)

// That way it can be mocked in tests
var GetHttpClientFunction = getHttpClient

// ProvisionPaCForComponent does Pipelines as Code provision for the given component.
// Mainly, it creates PaC configuration merge request into the component source repositotiry.
// If GitHub PaC application is not configured, creates a webhook for PaC.
func (r *ComponentBuildReconciler) ProvisionPaCForComponent(ctx context.Context, component *appstudiov1alpha1.Component) (string, error) {
	log := ctrllog.FromContext(ctx).WithName("PaC-setup")
	ctx = ctrllog.IntoContext(ctx, log)

	log.Info("Starting Pipelines as Code provision for the Component")

	gitProvider, err := getGitProvider(*component)
	if err != nil {
		// Do not reconcile, because configuration must be fixed before it is possible to proceed.
		return "", err
	}
	repoUrl := getGitRepoUrl(*component)

	if strings.HasPrefix(repoUrl, "http:") {
		return "", boerrors.NewBuildOpError(boerrors.EHttpUsedForRepository,
			fmt.Errorf("Git repository URL can't use insecure HTTP: %s", repoUrl))
	}

	if url, ok := component.Annotations[GitProviderAnnotationURL]; ok {
		if strings.HasPrefix(url, "http:") {
			return "", boerrors.NewBuildOpError(boerrors.EHttpUsedForRepository,
				fmt.Errorf("Git repository URL in annotation %s can't use insecure HTTP: %s", GitProviderAnnotationURL, repoUrl))
		}
	}

	pacSecret, err := r.lookupPaCSecret(ctx, component, gitProvider)
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
	if !IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
		// Generate webhook secret for the component git repository if not yet generated
		// and stores it in the corresponding k8s secret.
		webhookSecretString, err = r.ensureWebhookSecret(ctx, component)
		if err != nil {
			return "", err
		}

		// Obtain Pipelines as Code callback URL
		webhookTargetUrl, err = r.getPaCWebhookTargetUrl(ctx, repoUrl, true)
		if err != nil {
			return "", err
		}
	}

	if err := r.ensurePaCRepository(ctx, component, pacSecret); err != nil {
		return "", err
	}

	// Manage merge request for Pipelines as Code configuration
	mrUrl, err := r.ConfigureRepositoryForPaC(ctx, component, pacSecret.Data, webhookTargetUrl, webhookSecretString)
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
	if IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
		if gitProvider == "github" {
			// GitHub application
			err := checkMandatoryFieldsNotEmpty(pacSecret.Data, []string{PipelinesAsCodeGithubAppIdKey, PipelinesAsCodeGithubPrivateKey})
			if err != nil {
				return err
			}

			// validate content of the fields
			if _, e := strconv.ParseInt(string(pacSecret.Data[PipelinesAsCodeGithubAppIdKey]), 10, 64); e != nil {
				return fmt.Errorf(" Pipelines as Code: failed to parse GitHub application ID. Cause: %w", e)
			}

			privateKey := strings.TrimSpace(string(pacSecret.Data[PipelinesAsCodeGithubPrivateKey]))
			if !strings.HasPrefix(privateKey, "-----BEGIN RSA PRIVATE KEY-----") || // notsecret
				!strings.HasSuffix(privateKey, "-----END RSA PRIVATE KEY-----") {
				return fmt.Errorf(" Pipelines as Code secret: GitHub application private key is invalid")
			}
			return nil
		}
		return fmt.Errorf("There is no applications for %s", gitProvider)
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

func (r *ComponentBuildReconciler) TriggerPaCBuild(ctx context.Context, component *appstudiov1alpha1.Component) (bool, error) {
	log := ctrllog.FromContext(ctx).WithName("TriggerPaCBuild")
	ctx = ctrllog.IntoContext(ctx, log)

	repository, err := r.findPaCRepositoryForComponent(ctx, component)
	if err != nil {
		return false, err
	}

	if repository == nil {
		return false, fmt.Errorf("PaC repository not found for component %s", component.Name)
	}

	incomingSecret, reconcileRequired, err := r.ensureIncomingSecret(ctx, component)
	if err != nil {
		return false, err
	}

	repoUrl := getGitRepoUrl(*component)
	gitProvider, err := getGitProvider(*component)
	if err != nil {
		// There is no point to continue if git provider is not known.
		return false, err
	}

	pacSecret, err := r.lookupPaCSecret(ctx, component, gitProvider)
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

	secretValue := string(incomingSecret.Data[pacIncomingSecretKey][:])

	pipelineRunName := component.Name + pipelineRunOnPushSuffix

	triggerURL := fmt.Sprintf("%s/incoming", webhookTargetUrl)
	HttpClient := GetHttpClientFunction()

	// we have to supply source_url as additional param, because PaC isn't able to resolve it for trigger
	bytesParam := []byte(fmt.Sprintf("{\"params\": {\"source_url\": \"%s\"}, \"secret\": \"%s\", \"repository\": \"%s\", \"branch\": \"%s\", \"pipelinerun\": \"%s\", \"namespace\": \"%s\"}", repoUrl, secretValue, repository.Name, targetBranch, pipelineRunName, repository.Namespace))

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

// UndoPaCProvisionForComponent creates merge request that removes Pipelines as Code configuration from component source repository.
// Deletes PaC webhook if used.
// In case of any errors just logs them and does not block Component deletion.
func (r *ComponentBuildReconciler) UndoPaCProvisionForComponent(ctx context.Context, component *appstudiov1alpha1.Component) (string, error) {
	log := ctrllog.FromContext(ctx).WithName("PaC-cleanup")
	ctx = ctrllog.IntoContext(ctx, log)

	log.Info("Starting Pipelines as Code unprovision for the Component")

	gitProvider, err := getGitProvider(*component)
	if err != nil {
		// There is no point to continue if git provider is not known.
		return "", err
	}

	pacSecret, err := r.lookupPaCSecret(ctx, component, gitProvider)
	if err != nil {
		log.Error(err, "error getting git provider credentials secret", l.Action, l.ActionView)
		// Cannot continue without accessing git provider credentials.
		return "", boerrors.NewBuildOpError(boerrors.EPaCSecretNotFound, err)
	}

	repoUrl := getGitRepoUrl(*component)
	webhookTargetUrl := ""
	if !IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
		webhookTargetUrl, err = r.getPaCWebhookTargetUrl(ctx, repoUrl, true)
		if err != nil {
			// Just log the error and continue with pruning merge request creation
			log.Error(err, "failed to get Pipelines as Code webhook target URL. Webhook will not be deleted.", l.Action, l.ActionView, l.Audit, "true")
		}
	}

	// Manage merge request for Pipelines as Code configuration removal
	baseBranch, mrUrl, action, err := r.UnconfigureRepositoryForPaC(ctx, component, pacSecret.Data, webhookTargetUrl)
	if err != nil {
		log.Error(err, "failed to create merge request to remove Pipelines as Code configuration from Component source repository", l.Audit, "true")
		return "", err
	}

	err = r.cleanupPaCRepositoryIncomingsAndSecret(ctx, component, baseBranch)
	if err != nil {
		log.Error(err, "failed cleanup incomings from repo and incoming secret")
		return "", err
	}

	if action == "delete" {
		if mrUrl != "" {
			log.Info(fmt.Sprintf("Pipelines as Code configuration removal merge request: %s", mrUrl))
		} else {
			log.Info("Pipelines as Code configuration removal merge request is not needed")
		}
	} else if action == "close" {
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

func generateMergeRequestSourceBranch(component *appstudiov1alpha1.Component) string {
	return fmt.Sprintf("%s%s", pacMergeRequestSourceBranchPrefix, component.Name)
}

// getPipelineRunDefinitionFilePath returns full path in git repository to the pipeline run definition of the given Component.
func getPipelineRunDefinitionFilePath(component *appstudiov1alpha1.Component, isPullRequest bool) string {
	pipelineNameSuffix := pipelineRunOnPushFilename
	if isPullRequest {
		pipelineNameSuffix = pipelineRunOnPRFilename
	}
	return ".tekton/" + component.Name + "-" + pipelineNameSuffix
}

// ConfigureRepositoryForPaC creates a merge request with initial Pipelines as Code configuration
// and configures a webhook to notify in-cluster PaC unless application (on the repository side) is used.
func (r *ComponentBuildReconciler) ConfigureRepositoryForPaC(ctx context.Context, component *appstudiov1alpha1.Component, pacConfig map[string][]byte, webhookTargetUrl, webhookSecret string) (prUrl string, err error) {
	log := ctrllog.FromContext(ctx).WithValues("repository", component.Spec.Source.GitSource.URL)
	ctx = ctrllog.IntoContext(ctx, log)

	gitProvider, _ := getGitProvider(*component)
	repoUrl := getGitRepoUrl(*component)

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

	pipelineRunOnPushYaml, pipelineRunOnPRYaml, err := r.generatePaCPipelineRunConfigs(ctx, component, gitClient, baseBranch)
	if err != nil {
		return "", err
	}

	mrData := &gp.MergeRequestData{
		CommitMessage:  "Konflux update " + component.Name,
		SignedOff:      true,
		BranchName:     generateMergeRequestSourceBranch(component),
		BaseBranchName: baseBranch,
		Title:          "Konflux update " + component.Name,
		Text:           mergeRequestDescription,
		AuthorName:     "konflux",
		AuthorEmail:    "konflux@no-reply.konflux-ci.dev",
		Files: []gp.RepositoryFile{
			{FullPath: getPipelineRunDefinitionFilePath(component, false), Content: pipelineRunOnPushYaml},
			{FullPath: getPipelineRunDefinitionFilePath(component, true), Content: pipelineRunOnPRYaml},
		},
	}

	isAppUsed := IsPaCApplicationConfigured(gitProvider, pacConfig)
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
				webhookTargetUrl, component.GetName(), component.GetNamespace()),
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

// UnconfigureRepositoryForPaC creates a merge request that deletes Pipelines as Code configuration of the diven component in its repository.
// Deletes PaC webhook if it's used.
// Does not delete PaC GitHub application from the repository as its installation was done manually by the user.
// Returns merge request web URL or empty string if it's not needed.
func (r *ComponentBuildReconciler) UnconfigureRepositoryForPaC(ctx context.Context, component *appstudiov1alpha1.Component, pacConfig map[string][]byte, webhookTargetUrl string) (baseBranch string, prUrl string, action string, err error) {
	log := ctrllog.FromContext(ctx)

	gitProvider, _ := getGitProvider(*component)
	repoUrl := getGitRepoUrl(*component)

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

	isAppUsed := IsPaCApplicationConfigured(gitProvider, pacConfig)
	if !isAppUsed && webhookTargetUrl != "" {
		componentList := &appstudiov1alpha1.ComponentList{}
		if err := r.Client.List(ctx, componentList, &client.ListOptions{Namespace: component.Namespace}); err != nil {
			log.Error(err, "failed to list components")
			return "", "", "", err
		}

		sameRepoUsed := false
		for _, comp := range componentList.Items {
			if comp.Name == component.Name {
				continue
			}
			componentUrl := getGitRepoUrl(comp)
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
					webhookTargetUrl, component.GetName(), component.GetNamespace()),
					l.Action, l.ActionDelete)
			}
		}
	}

	sourceBranch := generateMergeRequestSourceBranch(component)
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
	if _, err := gitClient.DeleteBranch(repoUrl, sourceBranch); err != nil {
		return baseBranch, prUrl, action_done, err
	}
	log.Info(fmt.Sprintf("PaC configuration proposal branch %s is deleted", sourceBranch), l.Action, l.ActionDelete)

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
				{FullPath: getPipelineRunDefinitionFilePath(component, false)},
				{FullPath: getPipelineRunDefinitionFilePath(component, true)},
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

// getGitRepoUrl returns trimmed source url
func getGitRepoUrl(component appstudiov1alpha1.Component) string {
	return strings.TrimSuffix(strings.TrimSuffix(component.Spec.Source.GitSource.URL, "/"), ".git")
}

// validateGitSourceUrl validates if component.Spec.Source.GitSource.URL is valid git url
// https://github.com/owner/repository is valid
// https://github.com/owner is invalid
func validateGitSourceUrl(component appstudiov1alpha1.Component, gitProvider string) error {
	sourceUrl := getGitRepoUrl(component)
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

// getGitProvider returns git provider name based on the repository url or the git-provider annotation
func getGitProvider(component appstudiov1alpha1.Component) (string, error) {
	allowedGitProviders := []string{"github", "gitlab", "forgejo"}
	if component.Spec.Source.GitSource == nil {
		return "", boerrors.NewBuildOpError(boerrors.EWrongGitSourceUrl, fmt.Errorf("git source URL is not set for %s Component in %s namespace", component.Name, component.Namespace))
	}

	gitProvider := component.GetAnnotations()[GitProviderAnnotationName]
	if gitProvider != "" && !slices.Contains(allowedGitProviders, gitProvider) {
		return "", boerrors.NewBuildOpError(boerrors.EUnknownGitProvider, fmt.Errorf(`unsupported "%s" annotation value: %s`, GitProviderAnnotationName, gitProvider))
	}

	if gitProvider == "" {
		sourceUrl := component.Spec.Source.GitSource.URL
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
	err := validateGitSourceUrl(component, gitProvider)
	if err != nil {
		return "", boerrors.NewBuildOpError(boerrors.EWrongGitSourceUrl, err)
	}

	return gitProvider, nil
}
