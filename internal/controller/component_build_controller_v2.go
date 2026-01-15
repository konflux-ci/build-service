/*
Copyright 2021-2026 Red Hat, Inc.

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
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	"github.com/konflux-ci/build-service/pkg/boerrors"
	"github.com/konflux-ci/build-service/pkg/bometrics"
	"github.com/konflux-ci/build-service/pkg/git/gitprovider"
	l "github.com/konflux-ci/build-service/pkg/logs"
)

// PipelineDef represents a single pipeline definition with pointer fields
type PipelineDef struct {
	PipelineRefGit         *compapiv1alpha1.PipelineRefGit
	PipelineRefName        string
	PipelineSpecFromBundle *compapiv1alpha1.PipelineSpecFromBundle
	AdditionalParams       []string
}

// VersionPipelineDefinition represents pull and push pipeline definitions for a version
type VersionPipelineDefinition struct {
	Pull *PipelineDef
	Push *PipelineDef
}

// VersionInfo holds detailed information about a version
type VersionInfo struct {
	Context       string
	DockerfileURI string
	Revision      string
	SkipBuilds    bool

	OriginalVersion  string
	SanitizedVersion string
}

// onboardedVersionInfo holds version name and its merge URL
type onboardedVersionInfo struct {
	name  string
	mrUrl string
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components/status,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=pipelinesascode.tekton.dev,resources=repositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;patch;update;delete
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *ComponentBuildReconciler) ReconcileNewModel(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("ComponentOnboardingNewModel")
	ctx = ctrllog.IntoContext(ctx, log)
	reconcileStartTime := time.Now()

	// Fetch the Component instance
	var component compapiv1alpha1.Component
	err := r.Client.Get(ctx, req.NamespacedName, &component)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}
	log = log.WithValues("GitURL", component.Spec.Source.GitURL)
	ctx = ctrllog.IntoContext(ctx, log)

	// Don't recreate build pipeline Service Account upon component deletion.
	if component.ObjectMeta.DeletionTimestamp.IsZero() {
		// We need to make sure the Service Account exists before checking the Component image,
		// because Image Controller operator expects the Service Account to exist to link push secret to it.
		if err := r.EnsureBuildPipelineServiceAccount(ctx, &component, false); err != nil {
			log.Error(err, "Failed to ensure build pipeline service account")
			return ctrl.Result{}, err
		}
	}

	// Set message when git source is empty
	if component.Spec.Source.GitURL == "" {
		msg := fmt.Sprintf("Nothing to do for component without git source; gitURL: %s", component.Spec.Source.GitURL)
		log.Info(msg)
		return ctrl.Result{}, r.setStatusMessage(ctx, &component, msg)
	}

	// Container image must be set. It's not possible to proceed without it.
	containerImage := getContainerImageRepositoryForComponent(&component)
	if containerImage == "" {
		log.Info("Waiting for ContainerImage to be set")
		return ctrl.Result{}, r.setStatusMessage(ctx, &component, waitForContainerImageMessage)
	}

	if !component.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&component, PaCProvisionFinalizer) {
			// In order to not to block the deletion of the Component,
			// delete finalizer unconditionally and then try to do clean up ignoring errors.

			// Delete Pipelines as Code provision finalizer
			controllerutil.RemoveFinalizer(&component, PaCProvisionFinalizer)
			if err := r.Client.Update(ctx, &component); err != nil {
				log.Error(err, "Failed to update component after removing PaC finalizer, transient error")
				return ctrl.Result{}, err
			}
			log.Info("PaC finalizer removed", l.Action, l.ActionDelete)

			// Best-effort cleanup: remove PaC webhook if not used by other components, delete PaC configurations from repository for each version (unless skipped), and clean repository incomings
			r.cleanupComponentPaCResources(ctx, &component)
		}

		// Clean up common build pipelines Role Binding
		if err := r.removeBuildPipelineServiceAccountBinding(ctx, &component); err != nil {
			log.Info("Error during component cleanup: failed to remove build pipeline service account binding", "error", err)
		}

		return ctrl.Result{}, nil
	}

	// Validate versions (names, revisions, uniqueness)
	validationErrors := validateVersions(&component)
	if len(validationErrors) > 0 {
		errMsg := fmt.Sprintf("Component versions validation failed: %s", strings.Join(validationErrors, "; "))
		log.Error(fmt.Errorf("validation failed"), errMsg, l.Action, l.ActionView, "ValidationErrors", validationErrors)

		return ctrl.Result{}, r.setStatusMessage(ctx, &component, errMsg)
	}

	// Create a map of existing version names to version information
	existingSpecVersions := buildVersionInfoMap(&component, false)

	// Determines which versions need to be onboarded and offboarded
	versionsToOnboard, versionsToOffboard := r.determineVersionsToOnboardAndOffboard(ctx, &component, existingSpecVersions)

	// Determine which versions need configuration creation
	versionsToCreatePRFor, invalidVersionsToCreatePRFor := r.determineVersionsToCreateConfigurationFor(ctx, &component, existingSpecVersions)

	// Determines which versions need to trigger build
	versionsToTriggerBuildFor, invalidVersionsToTriggerBuildFor := r.determineVersionsToTriggerBuildFor(ctx, &component, existingSpecVersions, versionsToOnboard, versionsToCreatePRFor)

	// Clean up invalid versions from actions immediately
	// This must happen before other processing to handle the case where ALL versions in an action are invalid
	if len(invalidVersionsToCreatePRFor) > 0 {
		if err := r.removeCreateConfigurationActions(ctx, &component, invalidVersionsToCreatePRFor, true, versionsToCreatePRFor); err != nil {
			log.Error(err, "Failed to remove invalid versions from create configuration actions")
			return ctrl.Result{}, err
		}
		log.Info("Removed invalid versions from create configuration actions", l.Action, l.ActionUpdate, "InvalidVersions", invalidVersionsToCreatePRFor)
	}

	if len(invalidVersionsToTriggerBuildFor) > 0 {
		if err := r.removeTriggerBuildActions(ctx, &component, invalidVersionsToTriggerBuildFor); err != nil {
			log.Error(err, "Failed to remove invalid versions from trigger build actions")
			return ctrl.Result{}, err
		}
		log.Info("Removed invalid versions from trigger build actions", l.Action, l.ActionUpdate, "InvalidVersions", invalidVersionsToTriggerBuildFor)
	}

	// Check if PAC repository needs to be created or updated
	createOrUpdateRepository := false
	if component.Status.PacRepository == "" {
		// Repository doesn't exist yet, need to create it
		createOrUpdateRepository = true
	} else if !equalRepositorySettings(component.Spec.RepositorySettings, component.Status.RepositorySettings) {
		// Repository exists but settings changed, need to update
		createOrUpdateRepository = true
		log.Info("Repository settings changed, need to update PAC Repository", l.Action, l.ActionUpdate, "PacRepository", component.Status.PacRepository, "SpecRepositorySettings", component.Spec.RepositorySettings, "StatusRepositorySettings", component.Status.RepositorySettings)
	}

	var webhookSecretString string
	var webhookTargetUrl string
	var pacSecret *corev1.Secret
	var gitClient gitprovider.GitProviderClient

	// Get webhook data and PaC secret and git client if any action needs to be performed
	if createOrUpdateRepository || len(versionsToOnboard) > 0 || len(versionsToOffboard) > 0 || len(versionsToTriggerBuildFor) > 0 || len(versionsToCreatePRFor) > 0 {
		webhookSecretString, webhookTargetUrl, pacSecret, err = r.GetWebhookDataAndPacSecret(ctx, &component)
		if err != nil {
			return ctrl.Result{}, r.handlePersistentError(ctx, &component, err, "Failed to get webhook data and PaC secret")
		}

		// Get git client for repository operations (webhook setup, configuration creation)
		gitClient, err = GetGitClient(&component, pacSecret.Data)
		if err != nil {
			return ctrl.Result{}, r.handlePersistentError(ctx, &component, err, "Failed to get git client")
		}
	}

	// Add finalizer and create/update Repository
	if createOrUpdateRepository || len(versionsToOnboard) > 0 || len(versionsToCreatePRFor) > 0 {
		// Add finalizer so we can clean up PaC resources (webhooks, repository configurations) upon component removal
		if !controllerutil.ContainsFinalizer(&component, PaCProvisionFinalizer) {
			controllerutil.AddFinalizer(&component, PaCProvisionFinalizer)
			if err := r.Client.Update(ctx, &component); err != nil {
				log.Error(err, "failed to add PaC finalizer to the Component", l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}
			log.Info("PaC finalizer added", l.Action, l.ActionUpdate)
			// Wait for cache to have the latest ResourceVersion to prevent race with newly triggered reconciles
			r.WaitForCacheUpdateRes(ctx, types.NamespacedName{Namespace: component.Namespace, Name: component.Name}, &component)
		}

		// Ensure PaC Repository exists and is properly configured
		var pacRepositoryName string
		if pacRepositoryName, err = r.ensurePaCRepository(ctx, &component, pacSecret, &component.Spec.RepositorySettings, true); err != nil {
			return ctrl.Result{}, r.handlePersistentError(ctx, &component, err, "Failed to ensure PaC repository")
		}
		log.Info("Using PaC repository", "PaCRepositoryName", pacRepositoryName)

		// Update component status with PaC repository information
		if err := r.setStatusPacRepository(ctx, &component, pacRepositoryName, &component.Spec.RepositorySettings); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Setup PaC webhook for the component git repository when GitHub/GitLab App is not configured
	// (App-based repos skip webhook setup as the app manages webhooks automatically)
	if len(versionsToOnboard) > 0 || len(versionsToCreatePRFor) > 0 {
		if err := r.SetupPacWebhookWhenAppNotUsed(ctx, &component, gitClient, pacSecret.Data, webhookTargetUrl, webhookSecretString); err != nil {
			return ctrl.Result{}, r.handlePersistentError(ctx, &component, err, "Failed to setup PaC webhook")
		}
	}

	// Perform trigger builds
	if len(versionsToTriggerBuildFor) > 0 {
		// Build list of unique target branches for prep
		targetBranches := make([]string, 0, len(versionsToTriggerBuildFor))
		for _, versionName := range versionsToTriggerBuildFor {
			branch := existingSpecVersions[versionName].Revision
			if !slices.Contains(targetBranches, branch) {
				targetBranches = append(targetBranches, branch)
			}
		}

		// Prepare for triggering builds (ensure incoming secret, update repository incomings)
		reconcileRequired, incomingSecret, repository, err := r.TriggerPaCBuildPreparation(ctx, &component, targetBranches)
		if err != nil {
			return ctrl.Result{}, r.handlePersistentError(ctx, &component, err, "Failed to prepare for triggering builds")
		}

		if reconcileRequired {
			// Requeue for new reconcile because TriggerPaCBuildPrep created or updated resources
			// (e.g., incoming secret, repository incomings) that need to be processed in a fresh
			// reconcile loop before we can proceed with triggering builds
			log.Info("Reconcile required after TriggerPaCBuildPrep, requeuing")
			return ctrl.Result{Requeue: true}, nil
		}

		// Trigger builds for the requested versions
		successfullyTriggered := make([]string, 0, len(versionsToTriggerBuildFor))
		for _, versionName := range versionsToTriggerBuildFor {
			versionInfo := existingSpecVersions[versionName]
			// Trigger PaC build by calling the incoming webhook endpoint
			err := r.TriggerPaCBuild(ctx, &component, versionInfo.SanitizedVersion, versionInfo.Revision, webhookTargetUrl, incomingSecret, repository)
			if err != nil {
				log.Error(err, "Failed to trigger build for component version", "Version", versionName)

				// Check if error is persistent
				isErrPersistent := false
				var boErr *boerrors.BuildOpError
				if errors.As(err, &boErr) {
					isErrPersistent = boErr.IsPersistent()
				}

				// First, handle boerrors and set status message if persistent
				errorContext := fmt.Sprintf("Failed to trigger build for component %s version %s", component.Name, versionName)
				statusUpdateErr := r.handlePersistentError(ctx, &component, err, errorContext)

				// Then, remove successfully triggered builds and failed one with permanent error from actions
				// Removal from actions should be handled first and then report error when setting status on permanent error
				if len(successfullyTriggered) > 0 || isErrPersistent {
					if isErrPersistent {
						successfullyTriggered = append(successfullyTriggered, versionName)
					}
					if removeErr := r.removeTriggerBuildActions(ctx, &component, successfullyTriggered); removeErr != nil {
						log.Error(removeErr, "Failed to remove successfully triggered build actions for component", "SuccessfullyTriggered", successfullyTriggered)
						return ctrl.Result{}, removeErr
					}
					// Successfully removed actions - this triggered a new reconcile, so return success
					// to avoid double reconcile
					return ctrl.Result{}, nil
				}

				// No successful triggers to remove, return error to retry unless persistent handling was successful
				// When error was persistent and status successfully updated, error will be nil
				// When error was persistent and status wasn't updated successfully, error will be status update error
				// When error wasn't persistent, error will be original error
				return ctrl.Result{}, statusUpdateErr
			}

			successfullyTriggered = append(successfullyTriggered, versionName)
		}

		// All builds triggered successfully, remove all trigger actions
		if err := r.removeTriggerBuildActions(ctx, &component, versionsToTriggerBuildFor); err != nil {
			return ctrl.Result{}, err
		}

		// Observe metrics for successful build triggers
		r.observeActionMetrics("trigger-build", reconcileStartTime)

		log.Info("All builds triggered successfully", "TriggeredVersions", successfullyTriggered)
	}

	// Perform onboarding for all versions (adds them to status with OnboardingStatus and OnboardingTime)
	if len(versionsToOnboard) > 0 {
		onboardedVersions := make([]onboardedVersionInfo, 0, len(versionsToOnboard))
		for _, versionName := range versionsToOnboard {
			onboardedVersions = append(onboardedVersions, onboardedVersionInfo{name: versionName})
		}
		if err := r.updateVersionsStatus(ctx, &component, onboardedVersions, existingSpecVersions, true, ""); err != nil {
			log.Error(err, "Failed to update status for versions onboarding", "Versions", versionsToOnboard)
			return ctrl.Result{}, err
		}
		// Wait for cache to have the latest ResourceVersion to prevent race with newly triggered reconciles
		r.WaitForCacheUpdateRes(ctx, types.NamespacedName{Namespace: component.Namespace, Name: component.Name}, &component)

		// Observe metrics for successful onboarding
		r.observeActionMetrics("onboard", reconcileStartTime)

		log.Info("Successfully onboarded versions to status", "Versions", versionsToOnboard)
	}

	// Perform offboarding
	if len(versionsToOffboard) > 0 {
		if !component.Spec.SkipOffboardingPr {
			// Generate version info from status for offboarding
			existingStatusVersions := buildVersionInfoMap(&component, true)

			successfullyOffboarded := make([]string, 0, len(versionsToOffboard))
			for _, versionName := range versionsToOffboard {
				versionInfo := existingStatusVersions[versionName]
				// Remove PaC configuration for the version from the component git repository
				err := r.RemovePipelineRunsFromRepository(ctx, &component, versionInfo, gitClient, pacSecret.Data)
				if err != nil {
					log.Error(err, "Failed to undo PaC provision for component version", "Version", versionName)

					// Check if error is persistent
					isErrPersistent := false
					var versionErrorMsg string
					var boErr *boerrors.BuildOpError
					if errors.As(err, &boErr) {
						isErrPersistent = boErr.IsPersistent()
						versionErrorMsg = boErr.ShortError()
					} else {
						versionErrorMsg = err.Error()
					}

					// Set error message for the failed version only if error is persistent
					var statusUpdateErr error
					if isErrPersistent {
						if statusUpdateErr = r.setVersionStatusMessage(ctx, &component, versionName, versionErrorMsg); statusUpdateErr != nil {
							log.Error(statusUpdateErr, "Failed to set error message for failed offboarding version", "FailedVersion", versionName)
						}
					}

					// Remove successfully offboarded versions from status
					// Removal versions from status should be handled first and then report error when setting status on permanent error
					if len(successfullyOffboarded) > 0 {
						if removeErr := r.removeVersionsFromStatus(ctx, &component, successfullyOffboarded); removeErr != nil {
							log.Error(removeErr, "Failed to remove successfully offboarded versions from status", "SuccessfullyOffboarded", successfullyOffboarded)
							return ctrl.Result{}, removeErr
						}
					}

					// Return status update error if it occurred, otherwise return the original offboarding error
					if statusUpdateErr != nil {
						return ctrl.Result{}, statusUpdateErr
					}
					return ctrl.Result{}, err
				}

				successfullyOffboarded = append(successfullyOffboarded, versionName)
			}

			// Requested versions offboarded successfully, remove them from status
			if len(successfullyOffboarded) > 0 {
				if err := r.removeVersionsFromStatus(ctx, &component, successfullyOffboarded); err != nil {
					return ctrl.Result{}, err
				}
				log.Info("Requested versions offboarded successfully", "OffboardedVersions", successfullyOffboarded)
			}
		} else {
			// Removing PaC configurations was skipped, remove all offboarded versions from status
			if err := r.removeVersionsFromStatus(ctx, &component, versionsToOffboard); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Requested versions offboarded successfully", "OffboardedVersions", versionsToOffboard)
		}

		// Clean up repository incomings for offboarded versions
		// Don't return error here - since we already removed offboarded versions from status,
		// new reconcile won't enter the offboarding block again. The cleanup recalculates targets
		// from all components' status, so it will correct itself on the next offboarding of any
		// component using the same repository.
		if err = r.cleanupPaCRepositoryIncomings(ctx, &component); err != nil {
			log.Error(err, "failed to cleanup incomings from repository, will retry on next offboarding")
		}

		// Observe metrics for successful offboarding
		r.observeActionMetrics("offboard", reconcileStartTime)
	}

	// Perform create configuration PRs (for versions that need configuration)
	// These versions are already onboarded to status above, we just add the PR URL
	if len(versionsToCreatePRFor) > 0 {
		// Validate pipeline configurations and extract pipeline definitions
		pipelineValidationErrors, versionPipelines := validatePipelines(&component)
		if len(pipelineValidationErrors) > 0 {
			errMsg := fmt.Sprintf("Component pipelines validation failed: %s", strings.Join(pipelineValidationErrors, "; "))
			log.Error(fmt.Errorf("validation failed"), errMsg, "ValidationErrors", pipelineValidationErrors)

			return ctrl.Result{}, r.setStatusMessage(ctx, &component, errMsg)
		}

		// Filter pipelines for create configuration, validate default pipeline config and pipeline resolution
		pipelineConfigValidationErrors, err := r.resolvePipelinesForCreateConfiguration(ctx, versionPipelines, versionsToCreatePRFor)
		if err != nil {
			log.Error(err, "Failed to filter pipelines for create configuration")
			return ctrl.Result{}, err
		}
		if len(pipelineConfigValidationErrors) > 0 {
			errMsg := fmt.Sprintf("Pipeline configuration validation failed: %s", strings.Join(pipelineConfigValidationErrors, "; "))
			log.Error(fmt.Errorf("validation failed"), errMsg, "ValidationErrors", pipelineConfigValidationErrors)

			return ctrl.Result{}, r.setStatusMessage(ctx, &component, errMsg)
		}

		successfulWithPRs := make([]onboardedVersionInfo, 0, len(versionsToCreatePRFor))
		for _, versionName := range versionsToCreatePRFor {
			versionInfo := existingSpecVersions[versionName]
			// Create PaC pipeline configuration in the component git repository
			mrUrl, err := r.CreatePipelineRunsInRepository(ctx, &component, gitClient, versionInfo, versionPipelines[versionName], pacSecret.Data)
			if err != nil {
				log.Error(err, "Failed to create repository configuration for component version", "Version", versionName)

				// Check if error is persistent
				isErrPersistent := false
				var versionErrorMsg string
				var boErr *boerrors.BuildOpError
				if errors.As(err, &boErr) {
					isErrPersistent = boErr.IsPersistent()
					versionErrorMsg = boErr.ShortError()
				} else {
					versionErrorMsg = err.Error()
				}

				// Update status for successfully created configurations
				if len(successfulWithPRs) > 0 {
					if updateErr := r.updateVersionsStatus(ctx, &component, successfulWithPRs, existingSpecVersions, true, ""); updateErr != nil {
						log.Error(updateErr, "Failed to update status for successful configurations", "SuccessfulWithPRs", successfulWithPRs)
						return ctrl.Result{}, updateErr
					}
				}

				// Update status for the failed version with error message only if error is persistent
				if isErrPersistent {
					if updateErr := r.updateVersionsStatus(ctx, &component, []onboardedVersionInfo{{name: versionName}}, existingSpecVersions, false, versionErrorMsg); updateErr != nil {
						log.Error(updateErr, "Failed to update status for failed version", "FailedVersion", versionName)
						return ctrl.Result{}, updateErr
					}
				}

				// Remove successfully created configurations and failed one with persistent error from actions
				if len(successfulWithPRs) > 0 || isErrPersistent {
					versionNamesToRemove := make([]string, 0, len(successfulWithPRs)+1)
					for _, v := range successfulWithPRs {
						versionNamesToRemove = append(versionNamesToRemove, v.name)
					}
					// Remove also version which failed with persistent error, so next reconcile can continue with other versions
					if isErrPersistent {
						versionNamesToRemove = append(versionNamesToRemove, versionName)
					}
					if removeErr := r.removeCreateConfigurationActions(ctx, &component, versionNamesToRemove, false, versionsToCreatePRFor); removeErr != nil {
						log.Error(removeErr, "Failed to remove create configuration actions", "VersionsWithPRs", versionNamesToRemove)
						return ctrl.Result{}, removeErr
					}
					// Successfully removed actions - this triggered a new reconcile, so return success
					// to avoid double reconcile
					return ctrl.Result{}, nil
				}

				// No successful configurations to remove, return error to retry unless persistent error
				if isErrPersistent {
					return ctrl.Result{}, nil
				}
				return ctrl.Result{}, err
			}

			// when mrUrl is empty, pipeline is up to date (no PR needed)
			successfulWithPRs = append(successfulWithPRs, onboardedVersionInfo{name: versionName, mrUrl: mrUrl})
		}

		// All configurations created successfully, update status and remove all create configuration actions
		if err := r.updateVersionsStatus(ctx, &component, successfulWithPRs, existingSpecVersions, true, ""); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.removeCreateConfigurationActions(ctx, &component, versionsToCreatePRFor, false, versionsToCreatePRFor); err != nil {
			return ctrl.Result{}, err
		}
		// Observe metrics for successful creating configuration PR
		r.observeActionMetrics("create-pr", reconcileStartTime)

		log.Info("All configurations created successfully", "VersionsWithPRs", versionsToCreatePRFor)
	}

	// Clear component-level error message on successful reconciliation
	if component.Status.Message != "" {
		if err := r.setStatusMessage(ctx, &component, ""); err != nil {
			return ctrl.Result{}, err
		}
		// Wait for cache to have the latest ResourceVersion to prevent race with newly triggered reconciles
		r.WaitForCacheUpdateRes(ctx, types.NamespacedName{Namespace: component.Namespace, Name: component.Name}, &component)
	}

	// TODO handle skip builds !!!! if it is already working in PaC
	log.Info("New model reconciliation completed successfully", l.Action, l.ActionView)

	return ctrl.Result{}, nil
}

// waitForCacheUpdate waits for the controller cache to contain the component with the latest Generation or ResourceVersion.
// The checkGeneration parameter determines which field to check:
//   - true: Wait for Generation field to be updated (used after spec updates to prevent duplicate actions)
//   - false: Wait for ResourceVersion field to be updated (used after status updates to prevent conflicts)
//
// Spec updates increment Generation and trigger a new reconcile via the UpdateFunc predicate.
// Without this wait for Generation, the newly triggered reconcile might fetch a stale component from cache,
// causing the same actions to be repeated (e.g., creating duplicate PRs, triggering duplicate builds).
//
// Status updates increment ResourceVersion. When a reconcile previously updated the spec (triggering a new reconcile),
// we need to wait for status updates to propagate before the new reconcile starts.
// Without this wait for ResourceVersion, the newly triggered reconcile might fetch a stale component,
// leading to conflicts when it tries to re-apply the same status change.
func (r *ComponentBuildReconciler) waitForCacheUpdate(ctx context.Context, namespace types.NamespacedName, component *compapiv1alpha1.Component, checkGeneration bool) {
	log := ctrllog.FromContext(ctx)

	var failureMessage string
	var originalGeneration int64
	var originalResourceVersion string

	if checkGeneration {
		originalGeneration = component.Generation
		failureMessage = "Failed to wait for cache to have latest Generation. Actions may be repeated in next reconcile."
	} else {
		originalResourceVersion = component.ResourceVersion
		failureMessage = "failed to wait for cache to have latest ResourceVersion. Status updates could conflict."
	}

	isComponentInCacheUpToDate := false
	for i := 0; i < 20; i++ {
		// Use a temporary component to avoid overwriting the caller's component variable with stale cache data
		cachedComponent := &compapiv1alpha1.Component{}
		if err := r.Client.Get(ctx, namespace, cachedComponent); err == nil {
			var upToDate bool
			if checkGeneration {
				upToDate = originalGeneration == cachedComponent.Generation
			} else {
				upToDate = originalResourceVersion == cachedComponent.ResourceVersion
			}

			if upToDate {
				// Cache now has the updated component with the new Generation/ResourceVersion.
				isComponentInCacheUpToDate = true
				break
			}
			// Cache still has outdated value, continue waiting.
		} else {
			if k8serrors.IsNotFound(err) {
				// Component was deleted, no need to wait for cache update.
				isComponentInCacheUpToDate = true
				break
			}
			log.Error(err, "failed to get the component", l.Action, l.ActionView)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !isComponentInCacheUpToDate {
		log.Info(failureMessage, l.Audit, "true")
	}
}

// WaitForCacheUpdateGen waits for the controller cache to contain the component with the latest Generation.
// This should be called after spec updates to ensure the cache has propagated before the reconcile completes.
func (r *ComponentBuildReconciler) WaitForCacheUpdateGen(ctx context.Context, namespace types.NamespacedName, component *compapiv1alpha1.Component) {
	r.waitForCacheUpdate(ctx, namespace, component, true)
}

// WaitForCacheUpdateRes waits for the controller cache to contain the component with the latest ResourceVersion.
// This should be called after status updates when the current reconcile previously updated the spec (which triggered a new reconcile).
func (r *ComponentBuildReconciler) WaitForCacheUpdateRes(ctx context.Context, namespace types.NamespacedName, component *compapiv1alpha1.Component) {
	r.waitForCacheUpdate(ctx, namespace, component, false)
}

// observeActionMetrics records duration metrics for completed component actions.
// Calculates elapsed time since reconcileStartTime and observes the appropriate metric based on action type.
// Supported actions:
//   - "onboard": Records ComponentOnboardingTimeMetric (initial component version onboarding)
//   - "create-pr": Records PipelinesAsCodeComponentProvisionTimeMetric (creating pipeline configuration PR)
//   - "trigger-build": Records PushPipelineRebuildTriggerTimeMetric
//   - "offboard": Records PipelinesAsCodeComponentUnconfigureTimeMetric
func (r *ComponentBuildReconciler) observeActionMetrics(action string, reconcileStartTime time.Time) {
	duration := time.Since(reconcileStartTime).Seconds()

	switch action {
	case "onboard":
		bometrics.ComponentOnboardingTimeMetric.Observe(duration)
	case "create-pr":
		bometrics.PipelinesAsCodeComponentProvisionTimeMetric.Observe(duration)
	case "trigger-build":
		bometrics.PushPipelineRebuildTriggerTimeMetric.Observe(duration)
	case "offboard":
		bometrics.PipelinesAsCodeComponentUnconfigureTimeMetric.Observe(duration)
	}
}

// handlePersistentError checks if the error is a persistent BuildOpError and handles it
// by logging and setting the component status message.
// Returns error, where error is from setStatusMessage if it's a persistent error,
// or the original error if not persistent.
func (r *ComponentBuildReconciler) handlePersistentError(ctx context.Context, component *compapiv1alpha1.Component, err error, errorContext string) error {
	log := ctrllog.FromContext(ctx)

	var boErr *boerrors.BuildOpError
	if errors.As(err, &boErr) && boErr.IsPersistent() {
		// Persistent error - set status message and don't reconcile again
		errMsg := fmt.Sprintf("%s: %s", errorContext, boErr.ShortError())
		log.Error(err, errorContext)

		if err := r.setStatusMessage(ctx, component, errMsg); err != nil {
			return err
		}
		r.WaitForCacheUpdateRes(ctx, types.NamespacedName{Namespace: component.Namespace, Name: component.Name}, component)
		return nil
	}

	log.Error(err, fmt.Sprintf("%s, transient error", errorContext))
	return err
}

// cleanupComponentPaCResources performs best-effort cleanup of PaC resources during component deletion.
// It removes PaC webhook if not used by other components, deletes PaC configurations from repository
// for each version (unless skipped), and cleans up repository incomings.
// All errors are logged but not returned, as the component finalizer has already been removed.
func (r *ComponentBuildReconciler) cleanupComponentPaCResources(ctx context.Context, component *compapiv1alpha1.Component) {
	log := ctrllog.FromContext(ctx)

	// Generate version info from status for offboarding
	existingStatusVersions := buildVersionInfoMap(component, true)

	// Get webhook data and PaC secret
	_, webhookTargetUrl, pacSecret, err := r.GetWebhookDataAndPacSecret(ctx, component)
	if err != nil {
		log.Info("Error during component cleanup: failed to get webhook data and PaC secret", "error", err)
	}

	// Get git client (only if pacSecret is available)
	if pacSecret != nil {
		gitClient, err := GetGitClient(component, pacSecret.Data)
		if err != nil {
			log.Info("Error during component cleanup: failed to get git client", "error", err)
		} else {
			// Remove webhook only when other component isn't using the same git repository
			if webhookTargetUrl != "" {
				if err := r.RemovePacWebhook(ctx, component, gitClient, pacSecret.Data, webhookTargetUrl); err != nil {
					log.Info("Error during component cleanup: failed to remove PaC webhook", "error", err)
				}
			}

			// Remove PaC configuration for all versions from the component git repository
			if len(existingStatusVersions) > 0 {
				if !component.Spec.SkipOffboardingPr {
					for versionName, versionInfo := range existingStatusVersions {
						if err := r.RemovePipelineRunsFromRepository(ctx, component, versionInfo, gitClient, pacSecret.Data); err != nil {
							log.Info("Error during component cleanup: failed to undo PaC provision", "error", err, "Version", versionName)
						}
					}
				}
			}
		}
	}

	// Clean up repository incomings for offboarded versions
	if err := r.cleanupPaCRepositoryIncomings(ctx, component); err != nil {
		log.Info("Error during component cleanup: failed to cleanup repository incomings", "error", err)
	}
}

// buildVersionInfoMap creates a map of version names to detailed version information.
// When fromStatus is true, builds from component.Status.Versions (for offboarding).
// When fromStatus is false, builds from component.Spec.Source.Versions (for onboarding/builds).
// Returns a map of version names to VersionInfo pointers.
func buildVersionInfoMap(component *compapiv1alpha1.Component, fromStatus bool) map[string]*VersionInfo {
	versionInfoMap := make(map[string]*VersionInfo)

	if fromStatus {
		// Build from status - only includes fields needed for offboarding
		for _, version := range component.Status.Versions {
			versionInfoMap[version.Name] = &VersionInfo{
				OriginalVersion:  version.Name,
				SanitizedVersion: sanitizeVersionName(version.Name),
				Revision:         version.Revision,
			}
		}
	} else {
		// Build from spec - includes all fields
		for _, version := range component.Spec.Source.Versions {
			dockerfileURI := version.DockerfileURI
			if dockerfileURI == "" {
				dockerfileURI = component.Spec.Source.DockerfileURI
			}
			if dockerfileURI == "" {
				dockerfileURI = "Dockerfile"
			}

			versionInfoMap[version.Name] = &VersionInfo{
				OriginalVersion:  version.Name,
				SanitizedVersion: sanitizeVersionName(version.Name),
				Context:          version.Context,
				DockerfileURI:    dockerfileURI,
				Revision:         version.Revision,
				SkipBuilds:       version.SkipBuilds,
			}
		}
	}

	return versionInfoMap
}

// getUniqueVersionsFromVersionFields extracts and validates version references from action version fields.
//
// This function reconciles the three possible ways an action can specify target versions (allVersions flag,
// singleVersion field, or multipleVersions array) and validates them against the actual versions defined
// in the component spec.
//
// Parameters:
// - allVersions: if true, selects all versions from existingSpecVersions (takes precedence over other fields)
// - singleVersion: single version name (ignored if allVersions is true)
// - multipleVersions: array of version names (ignored if allVersions is true)
// - existingSpecVersions: map of all valid versions currently defined in the component spec
//
// Returns:
// - validVersions: deduplicated list of version names that exist in existingSpecVersions
//   - If allVersions=true: all versions from existingSpecVersions
//   - If allVersions=false: only the specified versions (singleVersion/multipleVersions) that exist
//
// - invalidVersions: versions referenced in singleVersion/multipleVersions that don't exist in existingSpecVersions
//   - Used to identify stale version references that should be cleaned up from the action
//
// Note: Even when allVersions=true, singleVersion and multipleVersions are still validated to detect
// and report invalid entries for cleanup, though they don't affect the validVersions return value.
func getUniqueVersionsFromVersionFields(allVersions bool, singleVersion string, multipleVersions []string, existingSpecVersions map[string]*VersionInfo) (validVersions []string, invalidVersions []string) {
	validVersionsSet := make(map[string]bool)
	invalidVersionsSet := make(map[string]bool)

	if allVersions {
		// AllVersions has precedence - add all versions from spec
		for versionName := range existingSpecVersions {
			validVersionsSet[versionName] = true
		}
		// Still need to check single and multiple for invalid versions to report for cleanup
		if singleVersion != "" {
			if _, exists := existingSpecVersions[singleVersion]; !exists {
				invalidVersionsSet[singleVersion] = true
			}
		}
		for _, version := range multipleVersions {
			if version != "" {
				if _, exists := existingSpecVersions[version]; !exists {
					invalidVersionsSet[version] = true
				}
			}
		}
	} else {
		// Check single version
		if singleVersion != "" {
			if _, exists := existingSpecVersions[singleVersion]; exists {
				validVersionsSet[singleVersion] = true
			} else {
				invalidVersionsSet[singleVersion] = true
			}
		}

		// Check all versions in array
		for _, version := range multipleVersions {
			if version != "" {
				if _, exists := existingSpecVersions[version]; exists {
					validVersionsSet[version] = true
				} else {
					invalidVersionsSet[version] = true
				}
			}
		}
	}

	// Convert to slices
	validVersions = slices.Collect(maps.Keys(validVersionsSet))
	invalidVersions = slices.Collect(maps.Keys(invalidVersionsSet))

	return validVersions, invalidVersions
}

// determineVersionsToCreateConfigurationFor determines which versions need build pipeline configuration PR creation from component actions.
// Returns:
// - versionsToOnboardWithPR: valid versions to create configuration for
// - invalidVersionsToOnboardWithPR: invalid versions (not in spec) that should be removed from actions
func (r *ComponentBuildReconciler) determineVersionsToCreateConfigurationFor(
	ctx context.Context,
	component *compapiv1alpha1.Component,
	existingSpecVersions map[string]*VersionInfo,
) (versionsToOnboardWithPR []string, invalidVersionsToOnboardWithPR []string) {
	log := ctrllog.FromContext(ctx)

	createConfig := component.Spec.Actions.CreateConfiguration
	if createConfig.AllVersions || createConfig.Version != "" || len(createConfig.Versions) > 0 {
		versionsToOnboardWithPR, invalidVersionsToOnboardWithPR = getUniqueVersionsFromVersionFields(createConfig.AllVersions, createConfig.Version, createConfig.Versions, existingSpecVersions)

		if len(versionsToOnboardWithPR) > 0 {
			log.Info("Create configuration PR action detected", "VersionsToCreate", versionsToOnboardWithPR)
		}
	}
	// Sort for deterministic processing order
	slices.Sort(versionsToOnboardWithPR)
	return versionsToOnboardWithPR, invalidVersionsToOnboardWithPR
}

// resolvePipelinesForCreateConfiguration validates and resolves pipeline definitions for versions that need configuration creation.
// It validates the default pipeline config and resolves any missing pipeline definitions or "latest" bundle references.
// Returns:
// - validationErrors: validation error messages for persistent errors (if any)
// - error: transient errors that should trigger reconciliation retry
func (r *ComponentBuildReconciler) resolvePipelinesForCreateConfiguration(
	ctx context.Context,
	versionPipelines map[string]*VersionPipelineDefinition,
	versionsToCreatePRFor []string,
) ([]string, error) {
	log := ctrllog.FromContext(ctx)

	if len(versionsToCreatePRFor) > 0 {
		log.Info("Create pipeline configuration PR action detected", "VersionsToCreate", versionsToCreatePRFor)

		validationErrors, err := r.processDefaultPipelineConfig(ctx, versionPipelines, versionsToCreatePRFor)
		if err != nil {
			return nil, err
		}
		if len(validationErrors) > 0 {
			return validationErrors, nil
		}
	}

	return nil, nil
}

// equalRepositorySettings compares two RepositorySettings structs for equality.
// Returns true if the settings are equal, false otherwise.
// GithubAppTokenScopeRepos are compared as mathematical sets (order and duplicates don't matter).
func equalRepositorySettings(a, b compapiv1alpha1.RepositorySettings) bool {
	if a.CommentStrategy != b.CommentStrategy {
		return false
	}

	// Use maps to compare repos as sets (independent of order and duplicates)
	aSet := make(map[string]bool)
	for _, repo := range a.GithubAppTokenScopeRepos {
		aSet[repo] = true
	}

	bSet := make(map[string]bool)
	for _, repo := range b.GithubAppTokenScopeRepos {
		bSet[repo] = true
	}

	return reflect.DeepEqual(aSet, bSet)
}

// determineVersionsToOnboardAndOffboard determines which versions need to be onboarded and offboarded.
// Returns two slices: versions to onboard and versions to offboard.
func (r *ComponentBuildReconciler) determineVersionsToOnboardAndOffboard(
	ctx context.Context,
	component *compapiv1alpha1.Component,
	existingSpecVersions map[string]*VersionInfo,
) (versionsToOnboard []string, versionsToOffboard []string) {
	log := ctrllog.FromContext(ctx)

	// Create a slice of status version names
	var statusVersionNames []string
	for _, version := range component.Status.Versions {
		statusVersionNames = append(statusVersionNames, version.Name)
	}

	// Find versions to be onboarded (in spec but not in status)
	for versionName := range existingSpecVersions {
		if !slices.Contains(statusVersionNames, versionName) {
			versionsToOnboard = append(versionsToOnboard, versionName)
		}
	}

	// Find versions to be offboarded (in status but not in spec)
	for _, version := range component.Status.Versions {
		if _, exists := existingSpecVersions[version.Name]; !exists {
			versionsToOffboard = append(versionsToOffboard, version.Name)
		}
	}

	if len(versionsToOnboard) > 0 {
		log.Info("Versions to onboard detected", "VersionsToOnboard", versionsToOnboard)
	}

	if len(versionsToOffboard) > 0 {
		log.Info("Versions to offboard detected", "VersionsToOffboard", versionsToOffboard)
	}

	// Sort for deterministic processing order
	slices.Sort(versionsToOnboard)
	slices.Sort(versionsToOffboard)
	return versionsToOnboard, versionsToOffboard
}

// determineVersionsToTriggerBuildFor determines for which versions there is a request to trigger a push build.
// Returns:
// - versionsToTriggerBuild: valid versions that should trigger builds
// - invalidVersionsToTriggerBuild: all versions (invalid + being onboarded) that should be removed from trigger build actions
func (r *ComponentBuildReconciler) determineVersionsToTriggerBuildFor(
	ctx context.Context,
	component *compapiv1alpha1.Component,
	existingSpecVersions map[string]*VersionInfo,
	versionsToOnboardWithoutPR []string,
	versionsToOnboardWithPR []string,
) (versionsToTriggerBuild []string, invalidVersionsToTriggerBuild []string) {
	log := ctrllog.FromContext(ctx)

	if component.Spec.Actions.TriggerBuild != "" || len(component.Spec.Actions.TriggerBuilds) > 0 {
		var invalidVersionsNotInSpec []string
		versionsToTriggerBuild, invalidVersionsNotInSpec = getUniqueVersionsFromVersionFields(false, component.Spec.Actions.TriggerBuild, component.Spec.Actions.TriggerBuilds, existingSpecVersions)

		// Add invalid versions (not in spec) to remove list
		invalidVersionsToTriggerBuild = append(invalidVersionsToTriggerBuild, invalidVersionsNotInSpec...)

		// Filter out versions that are being onboarded (with or without configuration)
		var filteredTriggerVersions []string
		var filteredBeingOnboarded []string
		for _, version := range versionsToTriggerBuild {
			if slices.Contains(versionsToOnboardWithoutPR, version) || slices.Contains(versionsToOnboardWithPR, version) {
				filteredBeingOnboarded = append(filteredBeingOnboarded, version)
			} else {
				filteredTriggerVersions = append(filteredTriggerVersions, version)
			}
		}
		versionsToTriggerBuild = filteredTriggerVersions

		// Add filtered onboarding versions to remove list
		invalidVersionsToTriggerBuild = append(invalidVersionsToTriggerBuild, filteredBeingOnboarded...)

		if len(versionsToTriggerBuild) > 0 {
			log.Info("Build trigger action detected", "VersionsToTrigger", versionsToTriggerBuild)
		}

		if len(filteredBeingOnboarded) > 0 {
			log.Info("Filtered out versions being onboarded from trigger build actions", "FilteredVersions", filteredBeingOnboarded)
		}

		if len(invalidVersionsNotInSpec) > 0 {
			log.Info("Found invalid versions in trigger build actions", "InvalidVersions", invalidVersionsNotInSpec)
		}
	}

	// Sort for deterministic processing order
	slices.Sort(versionsToTriggerBuild)
	return versionsToTriggerBuild, invalidVersionsToTriggerBuild
}

// updateVersionsStatus updates or adds version status entries in component.Status.Versions.
// Sets OnboardingStatus, OnboardingTime, ConfigurationMergeURL, and Message fields.
// When success=true: sets OnboardingStatus to "succeeded", OnboardingTime to current time,
// ConfigurationMergeURL to version.mrUrl if provided (otherwise preserves existing), and clears Message.
// When success=false: sets OnboardingStatus to "failed", clears OnboardingTime,
// preserves existing ConfigurationMergeURL, and sets Message to errorMessage if provided.
//
// Returns an error if the status update fails.
func (r *ComponentBuildReconciler) updateVersionsStatus(ctx context.Context, component *compapiv1alpha1.Component, versions []onboardedVersionInfo, existingSpecVersions map[string]*VersionInfo, success bool, errorMessage string) error {
	log := ctrllog.FromContext(ctx)

	if len(versions) == 0 {
		return nil
	}

	// Create a map of existing status versions for quick lookup
	statusVersionMap := make(map[string]int)
	for i, v := range component.Status.Versions {
		statusVersionMap[v.Name] = i
	}

	// Update or add version status for each version
	for _, version := range versions {
		versionInfo := existingSpecVersions[version.name]
		versionStatus := compapiv1alpha1.ComponentVersionStatus{
			Name:     version.name,
			Revision: versionInfo.Revision,
		}

		// Get existing ConfigurationMergeURL if version already versionExists in status
		existingConfigURL := ""
		versionIndex, versionExists := statusVersionMap[version.name]
		if versionExists {
			existingConfigURL = component.Status.Versions[versionIndex].ConfigurationMergeURL
		}

		if success {
			versionStatus.OnboardingStatus = "succeeded"
			versionStatus.OnboardingTime = time.Now().Format(time.RFC1123)
			if version.mrUrl != "" {
				versionStatus.ConfigurationMergeURL = version.mrUrl
			} else {
				versionStatus.ConfigurationMergeURL = existingConfigURL
			}
			// Clear any previous error message
			versionStatus.Message = ""
		} else {
			versionStatus.OnboardingStatus = "failed"
			versionStatus.OnboardingTime = ""
			versionStatus.ConfigurationMergeURL = existingConfigURL
			if errorMessage != "" {
				versionStatus.Message = errorMessage
			}
		}

		if versionExists {
			// Update existing version status
			component.Status.Versions[versionIndex] = versionStatus
		} else {
			// Add new version status
			component.Status.Versions = append(component.Status.Versions, versionStatus)
		}
	}

	// Update the component status
	if err := r.Client.Status().Update(ctx, component); err != nil {
		log.Error(err, "failed to update component version status", l.Action, l.ActionUpdate, l.Audit, "true")
		return err
	}

	return nil
}

// setStatusMessage sets provided message in the status and updates component's status.
// Returns an error if the status update fails.
func (r *ComponentBuildReconciler) setStatusMessage(ctx context.Context, component *compapiv1alpha1.Component, msg string) error {
	log := ctrllog.FromContext(ctx)

	if component.Status.Message == msg {
		return nil
	}

	component.Status.Message = msg
	if err := r.Client.Status().Update(ctx, component); err != nil {
		log.Error(err, "failed to update component status message", l.Action, l.ActionUpdate, l.Audit, "true")
		return err
	}
	return nil
}

// setStatusPacRepository updates the PaC repository name and settings in component status.
// Returns an error if the status update fails.
func (r *ComponentBuildReconciler) setStatusPacRepository(ctx context.Context, component *compapiv1alpha1.Component, repositoryName string, repositorySettings *compapiv1alpha1.RepositorySettings) error {
	log := ctrllog.FromContext(ctx)

	if component.Status.PacRepository == repositoryName && reflect.DeepEqual(component.Status.RepositorySettings, *repositorySettings) {
		return nil
	}

	component.Status.PacRepository = repositoryName
	component.Status.RepositorySettings = *repositorySettings

	if err := r.Client.Status().Update(ctx, component); err != nil {
		log.Error(err, "failed to update component status PaC repository", l.Action, l.ActionUpdate, l.Audit, "true")
		return err
	}
	// Wait for cache to have the latest ResourceVersion to prevent race with newly triggered reconciles
	r.WaitForCacheUpdateRes(ctx, types.NamespacedName{Namespace: component.Namespace, Name: component.Name}, component)
	return nil
}

// removeCreateConfigurationActions removes specified versions from component's CreateConfiguration actions.
//
// Behavior depends on whether AllVersions is set and whether we're removing invalid or successfully processed versions:
//
// 1. When AllVersions=true:
//   - removingInvalidVersions=true: Not possible - AllVersions expands to all valid spec versions, so no invalid versions exist
//   - removingInvalidVersions=false: Converts AllVersions to explicit list of remaining unprocessed versions
//   - Sets AllVersions=false
//   - Clears Version and Versions (since AllVersions had precedence)
//   - Calculates remaining versions as: versionsToCreatePRFor - versionsToRemove
//   - Sets remaining versions to Versions array
//
// 2. When AllVersions=false:
//   - Removes versionsToRemove from Version field (if it matches)
//   - Removes versionsToRemove from Versions array (filters out matching entries)
//   - Works the same regardless of removingInvalidVersions flag
//
// Returns an error if the component update fails.
func (r *ComponentBuildReconciler) removeCreateConfigurationActions(ctx context.Context, component *compapiv1alpha1.Component, versionsToRemove []string, removingInvalidVersions bool, versionsToCreatePRFor []string) error {
	log := ctrllog.FromContext(ctx)

	if len(versionsToRemove) == 0 {
		return nil
	}

	modified := false

	// Handle AllVersions field
	if component.Spec.Actions.CreateConfiguration.AllVersions {
		// Don't touch AllVersions when removing invalid versions - AllVersions means "all valid versions"
		if !removingInvalidVersions {
			// Removing successfully processed versions - convert AllVersions to explicit list of remaining unprocessed versions
			component.Spec.Actions.CreateConfiguration.AllVersions = false

			// Clear Version and Versions since AllVersions has precedence over them
			component.Spec.Actions.CreateConfiguration.Version = ""
			component.Spec.Actions.CreateConfiguration.Versions = nil

			// Calculate remaining versions: versionsToCreatePRFor - versionsToRemove
			remainingVersions := make([]string, 0)
			for _, version := range versionsToCreatePRFor {
				if !slices.Contains(versionsToRemove, version) {
					remainingVersions = append(remainingVersions, version)
				}
			}

			// Set the remaining versions to Versions array
			if len(remainingVersions) > 0 {
				component.Spec.Actions.CreateConfiguration.Versions = remainingVersions
			}

			modified = true
		}
	} else {
		// AllVersions is false, handle Version and Versions fields directly

		// Remove from Version (single version field)
		if component.Spec.Actions.CreateConfiguration.Version != "" && slices.Contains(versionsToRemove, component.Spec.Actions.CreateConfiguration.Version) {
			component.Spec.Actions.CreateConfiguration.Version = ""
			modified = true
		}

		// Remove from Versions (array field)
		if len(component.Spec.Actions.CreateConfiguration.Versions) > 0 {
			newVersions := make([]string, 0, len(component.Spec.Actions.CreateConfiguration.Versions))
			for _, version := range component.Spec.Actions.CreateConfiguration.Versions {
				if !slices.Contains(versionsToRemove, version) {
					newVersions = append(newVersions, version)
				}
			}
			if len(newVersions) != len(component.Spec.Actions.CreateConfiguration.Versions) {
				component.Spec.Actions.CreateConfiguration.Versions = newVersions
				modified = true
			}
		}
	}

	if !modified {
		return nil
	}

	if err := r.Client.Update(ctx, component); err != nil {
		log.Error(err, "failed to update component after removing create configuration actions", l.Action, l.ActionUpdate, l.Audit, "true")
		return err
	}
	r.WaitForCacheUpdateGen(ctx, types.NamespacedName{Namespace: component.Namespace, Name: component.Name}, component)

	log.Info("Removed create configuration actions", "RemovedVersions", versionsToRemove, l.Action, l.ActionUpdate)
	return nil
}

// removeTriggerBuildActions removes specified versions from component's TriggerBuild and TriggerBuilds actions.
// Returns an error if the component update fails.
func (r *ComponentBuildReconciler) removeTriggerBuildActions(ctx context.Context, component *compapiv1alpha1.Component, versionsToRemove []string) error {
	log := ctrllog.FromContext(ctx)

	if len(versionsToRemove) == 0 {
		return nil
	}

	modified := false

	// Remove from TriggerBuild (single version field)
	if component.Spec.Actions.TriggerBuild != "" && slices.Contains(versionsToRemove, component.Spec.Actions.TriggerBuild) {
		component.Spec.Actions.TriggerBuild = ""
		modified = true
	}

	// Remove from TriggerBuilds (array field)
	if len(component.Spec.Actions.TriggerBuilds) > 0 {
		newTriggerBuilds := make([]string, 0, len(component.Spec.Actions.TriggerBuilds))
		for _, version := range component.Spec.Actions.TriggerBuilds {
			if !slices.Contains(versionsToRemove, version) {
				newTriggerBuilds = append(newTriggerBuilds, version)
			}
		}
		if len(newTriggerBuilds) != len(component.Spec.Actions.TriggerBuilds) {
			component.Spec.Actions.TriggerBuilds = newTriggerBuilds
			modified = true
		}
	}

	if !modified {
		return nil
	}

	if err := r.Client.Update(ctx, component); err != nil {
		log.Error(err, "failed to update component after removing trigger build actions", l.Action, l.ActionUpdate, l.Audit, "true")
		return err
	}
	r.WaitForCacheUpdateGen(ctx, types.NamespacedName{Namespace: component.Namespace, Name: component.Name}, component)

	log.Info("Removed trigger build actions", "RemovedVersions", versionsToRemove, l.Action, l.ActionUpdate)
	return nil
}

// removeVersionsFromStatus removes specified versions from component.Status.Versions.
// Returns an error if the status update fails.
func (r *ComponentBuildReconciler) removeVersionsFromStatus(ctx context.Context, component *compapiv1alpha1.Component, versionsToRemove []string) error {
	log := ctrllog.FromContext(ctx)

	if len(versionsToRemove) == 0 {
		return nil
	}

	// Filter out versions to remove
	newVersions := make([]compapiv1alpha1.ComponentVersionStatus, 0, len(component.Status.Versions))
	for _, version := range component.Status.Versions {
		if !slices.Contains(versionsToRemove, version.Name) {
			newVersions = append(newVersions, version)
		}
	}

	// Check if any versions were actually removed
	if len(newVersions) == len(component.Status.Versions) {
		return nil
	}

	component.Status.Versions = newVersions

	if err := r.Client.Status().Update(ctx, component); err != nil {
		log.Error(err, "failed to update component status after removing versions", l.Action, l.ActionUpdate, l.Audit, "true")
		return err
	}
	// Wait for cache to have the latest ResourceVersion to prevent race with newly triggered reconciles
	r.WaitForCacheUpdateRes(ctx, types.NamespacedName{Namespace: component.Namespace, Name: component.Name}, component)

	log.Info("Removed versions from status", "RemovedVersions", versionsToRemove, l.Action, l.ActionUpdate)
	return nil
}

// setVersionStatusMessage sets the message field for a specific version in component.Status.Versions.
// Returns an error if the status update fails.
func (r *ComponentBuildReconciler) setVersionStatusMessage(ctx context.Context, component *compapiv1alpha1.Component, versionName string, message string) error {
	log := ctrllog.FromContext(ctx)

	// Find the version in status
	found := false
	for i := range component.Status.Versions {
		if component.Status.Versions[i].Name == versionName {
			if component.Status.Versions[i].Message == message {
				return nil // Message already set, no update needed
			}
			component.Status.Versions[i].Message = message
			found = true
			break
		}
	}

	if !found {
		log.Info("Version not found in status, cannot set message", "Version", versionName)
		return nil
	}

	if err := r.Client.Status().Update(ctx, component); err != nil {
		log.Error(err, "failed to update version message in component status", l.Action, l.ActionUpdate, l.Audit, "true")
		return err
	}

	log.Info("Set version message in status", "Version", versionName, "Message", message, l.Action, l.ActionUpdate)
	return nil
}
