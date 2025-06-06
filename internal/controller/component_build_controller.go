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
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	"github.com/konflux-ci/build-service/pkg/bometrics"
	"github.com/konflux-ci/build-service/pkg/k8s"
	l "github.com/konflux-ci/build-service/pkg/logs"
	pacwebhook "github.com/konflux-ci/build-service/pkg/pacwebhook"
)

const (
	BuildRequestAnnotationName                  = "build.appstudio.openshift.io/request"
	BuildRequestTriggerPaCBuildAnnotationValue  = "trigger-pac-build"
	BuildRequestConfigurePaCAnnotationValue     = "configure-pac"
	BuildRequestConfigurePaCNoMrAnnotationValue = "configure-pac-no-mr"
	BuildRequestUnconfigurePaCAnnotationValue   = "unconfigure-pac"

	BuildStatusAnnotationName = "build.appstudio.openshift.io/status"

	PaCProvisionFinalizer            = "pac.component.appstudio.openshift.io/finalizer"
	ImageRegistrySecretLinkFinalizer = "image-registry-secret-sa-link.component.appstudio.openshift.io/finalizer"

	ApplicationNameLabelName  = "appstudio.openshift.io/application"
	ComponentNameLabelName    = "appstudio.openshift.io/component"
	PartOfLabelName           = "app.kubernetes.io/part-of"
	PartOfAppStudioLabelValue = "appstudio"

	gitCommitShaAnnotationName    = "build.appstudio.redhat.com/commit_sha"
	gitRepoAtShaAnnotationName    = "build.appstudio.openshift.io/repo"
	gitTargetBranchAnnotationName = "build.appstudio.redhat.com/target_branch"

	buildPipelineServiceAccountName = "appstudio-pipeline"

	defaultBuildPipelineAnnotation     = "build.appstudio.openshift.io/pipeline"
	buildPipelineConfigMapResourceName = "build-pipeline-config"
	buildPipelineConfigName            = "config.yaml"
)

type BuildStatus struct {
	PaC *PaCBuildStatus `json:"pac,omitempty"`
	// Shows build methods agnostic messages, e.g. invalid build request.
	Message string `json:"message,omitempty"`
}

// Describes persistent error for build request.
type ErrorInfo struct {
	ErrId      int    `json:"error-id,omitempty"`
	ErrMessage string `json:"error-message,omitempty"`
}

type PaCBuildStatus struct {
	// State shows if PaC is used.
	// Values are: enabled, disabled.
	State string `json:"state,omitempty"`
	// Contains link to PaC provision / unprovision pull request
	MergeUrl string `json:"merge-url,omitempty"`
	// Time of the last successful PaC configuration in RFC1123 format
	ConfigurationTime string `json:"configuration-time,omitempty"`

	ErrorInfo
}

// ComponentBuildReconciler watches AppStudio Component objects in order to
// provision Pipelines as Code configuration for the Component or
// submit initial builds and dependent resources if PaC is not configured.
type ComponentBuildReconciler struct {
	Client             client.Client
	Scheme             *runtime.Scheme
	EventRecorder      record.EventRecorder
	CredentialProvider *k8s.GitCredentialProvider
	WebhookURLLoader   pacwebhook.WebhookURLLoader
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appstudiov1alpha1.Component{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return true
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		})).
		Named("ComponentOnboarding").
		Complete(r)
}

func updateMetricsTimes(componentIdForMetrics string, requestedAction string, reconcileStartTime time.Time) {
	componentInfo, timeRecorded := bometrics.ComponentTimesForMetrics[componentIdForMetrics]

	// first reconcile
	if !timeRecorded {
		bometrics.ComponentTimesForMetrics[componentIdForMetrics] = bometrics.ComponentMetricsInfo{StartTimestamp: reconcileStartTime, RequestedAction: requestedAction}
	} else {
		// new different request
		if componentInfo.RequestedAction != requestedAction {
			bometrics.ComponentTimesForMetrics[componentIdForMetrics] = bometrics.ComponentMetricsInfo{StartTimestamp: reconcileStartTime, RequestedAction: requestedAction}
		}
	}
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=releaseplanadmissions,verbs=get;list;watch
//+kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=create
//+kubebuilder:rbac:groups=pipelinesascode.tekton.dev,resources=repositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
// The following line is needed for component_dependency_update_controller since config there is overriden from here.
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=create;get;list;watch;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;patch;update;delete
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch

func (r *ComponentBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("ComponentOnboarding")
	ctx = ctrllog.IntoContext(ctx, log)
	reconcileStartTime := time.Now()

	// Fetch the Component instance
	var component appstudiov1alpha1.Component
	err := r.Client.Get(ctx, req.NamespacedName, &component)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Don't recreate build pipeline Service Account upon component deletion.
	if component.ObjectMeta.DeletionTimestamp.IsZero() {
		// Ensure deprecated pipeline service account exists for backward compatability.
		_, err = r.ensurePipelineServiceAccount(ctx, component.Namespace)
		if err != nil {
			return ctrl.Result{}, err
		}

		// We need to make sure the Service Account exists before checking the Component image,
		// because Image Controller operator expects the Service Account to exist to link push secret to it.
		if err := r.EnsureBuildPipelineServiceAccount(ctx, &component); err != nil {
			return ctrl.Result{}, err
		}

		// TODO remove after migration to dedicated Service Account is done.
		// It's needed to add newly linked common secrets to appstudio-pipeline into dedicated Service Account.
		if err := r.linkCommonAppstudioPipelineSecretsToNewServiceAccount(ctx, &component); err != nil {
			return ctrl.Result{}, err
		}
	}

	if getContainerImageRepositoryForComponent(&component) == "" {
		// Container image must be set. It's not possible to proceed without it.
		log.Info("Waiting for ContainerImage to be set")
		return ctrl.Result{}, nil
	}

	// Do not run any builds if component doesn't have gitsource
	if component.Spec.Source.GitSource == nil || component.Spec.Source.GitSource.URL == "" {
		log.Info("Nothing to do for container image component without source")
		return ctrl.Result{}, nil
	}

	log = log.WithValues("ComponentGitSource", component.Spec.Source.GitSource)
	ctx = ctrllog.IntoContext(ctx, log)

	componentIdForMetrics := fmt.Sprintf("%s=%s", component.Name, component.Namespace)

	if !component.ObjectMeta.DeletionTimestamp.IsZero() {
		// Deletion of the component is requested
		// remove component from metrics map
		delete(bometrics.ComponentTimesForMetrics, componentIdForMetrics)

		// can be removed in the future, just keeping it for backwards compatibility
		if controllerutil.ContainsFinalizer(&component, ImageRegistrySecretLinkFinalizer) {
			if err := r.Client.Get(ctx, req.NamespacedName, &component); err != nil {
				log.Error(err, "failed to get Component", l.Action, l.ActionView)
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&component, ImageRegistrySecretLinkFinalizer)
			if err := r.Client.Update(ctx, &component); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Image registry secret link finalizer removed", l.Action, l.ActionDelete)

			// A new reconcile will be triggered because of the update above
			return ctrl.Result{}, nil
		}

		if err := r.cleanUpNudgingPullSecrets(ctx, &component); err != nil {
			log.Error(err, "failed to clean up linked nudging pull secrets")
			return ctrl.Result{}, err
		}

		if controllerutil.ContainsFinalizer(&component, PaCProvisionFinalizer) {
			// In order to not to block the deletion of the Component,
			// delete finalizer unconditionally and then try to do clean up ignoring errors.

			// Delete Pipelines as Code provision finalizer
			controllerutil.RemoveFinalizer(&component, PaCProvisionFinalizer)
			if err := r.Client.Update(ctx, &component); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("PaC finalizer removed", l.Action, l.ActionDelete)

			// Try to clean up Pipelines as Code configuration
			_, _ = r.UndoPaCProvisionForComponent(ctx, &component)
		}

		// Clean up common build pipelines Role Binding
		if err := r.removeBuildPipelineServiceAccountBinding(ctx, &component); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// Migrate existing components to the new Service Accounts model.
	// TODO remove when the migration is done.
	if component.Annotations != nil && component.Annotations[serviceAccountMigrationAnnotationName] == "true" {
		if err := r.performServiceAccountMigration(ctx, &component); err != nil {
			if boErr, ok := err.(*boerrors.BuildOpError); !ok || !boErr.IsPersistent() {
				// Transient error, retry the migration
				return ctrl.Result{}, err
			}
			// Permanent error, cannot do more, stop.
			log.Error(err, "Migration to the new Service Account permanently failed")
		}
		delete(component.Annotations, serviceAccountMigrationAnnotationName)
		return ctrl.Result{}, r.Client.Update(ctx, &component)
	}

	_, _, err = r.GetBuildPipelineFromComponentAnnotation(ctx, &component)
	if err != nil {
		buildStatus := readBuildStatus(&component)
		// when reading pipeline annotation fails, we should end reconcile, unless transient error
		if boErr, ok := err.(*boerrors.BuildOpError); ok && boErr.IsPersistent() {
			buildStatus.Message = fmt.Sprintf("%d: %s", err.(*boerrors.BuildOpError).GetErrorId(), err.(*boerrors.BuildOpError).ShortError())
		} else {
			// transient error, retry
			return ctrl.Result{}, err
		}

		// when pipeline annotation is missing, we will update component with default annotation
		// so this reconcile will finish without error, and updated component will trigger new reconcile which already has pipeline annotation
		// we don't want to update neither status or remove annotation, because that will be handled by next reconcile
		missingPipelineAnnotationError := boerrors.NewBuildOpError(boerrors.EMissingPipelineAnnotation, nil)
		if err.(*boerrors.BuildOpError).GetErrorId() == missingPipelineAnnotationError.GetErrorId() {
			err = r.SetDefaultBuildPipelineComponentAnnotation(ctx, &component)
			if err == nil {
				return ctrl.Result{}, nil
			}

			if boErr, ok := err.(*boerrors.BuildOpError); ok && boErr.IsPersistent() {
				buildStatus.Message = fmt.Sprintf("%d: %s", err.(*boerrors.BuildOpError).GetErrorId(), err.(*boerrors.BuildOpError).ShortError())
			} else {
				// transient error, retry
				return ctrl.Result{}, err
			}
		}

		log.Error(err, fmt.Sprintf("Failed to read %s annotation on component %s", defaultBuildPipelineAnnotation, component.Name), l.Action, l.ActionView)
		writeBuildStatus(&component, buildStatus)
		delete(component.Annotations, BuildRequestAnnotationName)

		if err := r.Client.Update(ctx, &component); err != nil {
			log.Error(err, fmt.Sprintf("failed to update component after wrong %s annotation", defaultBuildPipelineAnnotation))
			return ctrl.Result{}, err
		}
		log.Info(fmt.Sprintf("updated component after wrong %s annotation", defaultBuildPipelineAnnotation))
		r.WaitForCacheUpdate(ctx, req.NamespacedName, &component)

		return ctrl.Result{}, nil
	}

	requestedAction, requestedActionExists := component.Annotations[BuildRequestAnnotationName]
	if !requestedActionExists {
		if _, statusExists := component.Annotations[BuildStatusAnnotationName]; statusExists {
			// Nothing to do
			return ctrl.Result{}, nil
		}
		// Automatically build component after creation
		log.Info("automatically requesting pac provision for the new component")
		requestedAction = BuildRequestConfigurePaCAnnotationValue
	}

	switch requestedAction {
	case BuildRequestTriggerPaCBuildAnnotationValue:
		updateMetricsTimes(componentIdForMetrics, requestedAction, reconcileStartTime)

		buildStatus := readBuildStatus(&component)
		if !(buildStatus.PaC != nil && buildStatus.PaC.State == "enabled") {
			log.Info("Can't rerun push pipeline because Pipelines as Code isn't provisioned for the Component")
			return ctrl.Result{}, nil
		}

		reconcileRequired, err := r.TriggerPaCBuild(ctx, &component)

		if err != nil {
			if boErr, ok := err.(*boerrors.BuildOpError); ok && boErr.IsPersistent() {
				log.Error(err, "Failed to rerun push pipeline for the Component")
				buildStatus := readBuildStatus(&component)
				buildStatus.PaC.ErrId = boErr.GetErrorId()
				buildStatus.PaC.ErrMessage = boErr.ShortError()
				writeBuildStatus(&component, buildStatus)
			} else {
				// transient error, retry
				log.Error(err, "Failed to rerun push pipeline for the Component with transient error")
				return ctrl.Result{}, err
			}
		} else {
			if reconcileRequired {
				return ctrl.Result{Requeue: true}, nil
			}
			bometrics.PushPipelineRebuildTriggerTimeMetric.Observe(time.Since(bometrics.ComponentTimesForMetrics[componentIdForMetrics].StartTimestamp).Seconds())
		}

	case BuildRequestConfigurePaCAnnotationValue, BuildRequestConfigurePaCNoMrAnnotationValue:
		updateMetricsTimes(componentIdForMetrics, requestedAction, reconcileStartTime)
		// initial build upon component creation (doesn't have either build status)
		initialBuild := func() bool {
			initialBuildStatus := readBuildStatus(&component)
			return initialBuildStatus.PaC == nil
		}()

		pacBuildStatus := &PaCBuildStatus{}
		if mergeUrl, err := r.ProvisionPaCForComponent(ctx, &component); err != nil {
			if boErr, ok := err.(*boerrors.BuildOpError); ok && boErr.IsPersistent() {
				log.Error(err, "Pipelines as Code provision for the Component failed")
				pacBuildStatus.State = "error"
				pacBuildStatus.ErrId = boErr.GetErrorId()
				pacBuildStatus.ErrMessage = boErr.ShortError()
			} else {
				// transient error, retry
				log.Error(err, "Pipelines as Code provision transient error")
				return ctrl.Result{}, err
			}
		} else {
			pacBuildStatus.State = "enabled"
			pacBuildStatus.MergeUrl = mergeUrl
			pacBuildStatus.ConfigurationTime = time.Now().Format(time.RFC1123)
			log.Info("Pipelines as Code provision for the Component finished successfully")

			// initial PaC provision upon component creation
			if initialBuild {
				bometrics.ComponentOnboardingTimeMetric.Observe(time.Since(bometrics.ComponentTimesForMetrics[componentIdForMetrics].StartTimestamp).Seconds())
			} else {
				// if PaC is up to date, don't count request
				if mergeUrl != "" {
					bometrics.PipelinesAsCodeComponentProvisionTimeMetric.Observe(time.Since(bometrics.ComponentTimesForMetrics[componentIdForMetrics].StartTimestamp).Seconds())
				}
			}
		}

		// Update component to reflect Pipeline as Code provision status
		if err := r.Client.Get(ctx, req.NamespacedName, &component); err != nil {
			log.Error(err, "failed to get Component", l.Action, l.ActionView)
			return ctrl.Result{}, err
		}

		// Add finalizer to clean up Pipelines as Code configuration on component deletion
		if component.ObjectMeta.DeletionTimestamp.IsZero() && pacBuildStatus.ErrId == 0 {
			if !controllerutil.ContainsFinalizer(&component, PaCProvisionFinalizer) {
				controllerutil.AddFinalizer(&component, PaCProvisionFinalizer)
				log.Info("adding PaC finalizer")
			}
		}

		// Update build status annotation
		buildStatus := readBuildStatus(&component)
		buildStatus.PaC = pacBuildStatus
		buildStatus.Message = "done"
		writeBuildStatus(&component, buildStatus)

		// Update PaC annotation
		if len(component.Annotations) == 0 {
			component.Annotations = make(map[string]string)
		}

	case BuildRequestUnconfigurePaCAnnotationValue:
		updateMetricsTimes(componentIdForMetrics, requestedAction, reconcileStartTime)

		// Remove Pipelines as Code configuration finalizer
		if controllerutil.ContainsFinalizer(&component, PaCProvisionFinalizer) {
			controllerutil.RemoveFinalizer(&component, PaCProvisionFinalizer)
			if err := r.Client.Update(ctx, &component); err != nil {
				log.Error(err, "failed to remove PaC finalizer to the Component", l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			} else {
				log.Info("PaC finalizer removed", l.Action, l.ActionUpdate)
			}
		}

		pacBuildStatus := &PaCBuildStatus{}
		if mergeUrl, err := r.UndoPaCProvisionForComponent(ctx, &component); err != nil {
			if boErr, ok := err.(*boerrors.BuildOpError); ok && boErr.IsPersistent() {
				log.Error(err, "Pipelines as Code unprovision for the Component failed")
				pacBuildStatus.State = "error"
				pacBuildStatus.ErrId = boErr.GetErrorId()
				pacBuildStatus.ErrMessage = boErr.ShortError()
			} else {
				// transient error, retry
				log.Error(err, "Pipelines as Code unprovision transient error")
				return ctrl.Result{}, err
			}
		} else {
			pacBuildStatus.State = "disabled"
			pacBuildStatus.MergeUrl = mergeUrl
			log.Info("Pipelines as Code unprovision for the Component finished successfully")

			// if PaC doesn't require unprovision, don't count request
			if mergeUrl != "" {
				bometrics.PipelinesAsCodeComponentUnconfigureTimeMetric.Observe(time.Since(bometrics.ComponentTimesForMetrics[componentIdForMetrics].StartTimestamp).Seconds())
			}
		}

		// Update component to show Pipeline as Code provision is undone
		if err := r.Client.Get(ctx, req.NamespacedName, &component); err != nil {
			log.Error(err, "failed to get Component", l.Action, l.ActionView)
			return ctrl.Result{}, err
		}

		// Update build status annotation
		buildStatus := readBuildStatus(&component)
		buildStatus.PaC = pacBuildStatus
		buildStatus.Message = "done"
		writeBuildStatus(&component, buildStatus)

	default:
		if requestedAction == "" {
			// Do not show error for empty annotation, consider it as noop.
			return ctrl.Result{}, nil
		}

		buildStatus := readBuildStatus(&component)
		buildStatus.Message = fmt.Sprintf("unexpected build request: %s", requestedAction)
		writeBuildStatus(&component, buildStatus)
	}

	delete(component.Annotations, BuildRequestAnnotationName)

	if err := r.Client.Update(ctx, &component); err != nil {
		log.Error(err, fmt.Sprintf("failed to update component after build request: %s", requestedAction), l.Action, l.ActionUpdate, l.Audit, "true")
		return ctrl.Result{}, err
	}
	log.Info(fmt.Sprintf("updated component after build request: %s", requestedAction), l.Action, l.ActionUpdate)
	// remove component from metrics map
	delete(bometrics.ComponentTimesForMetrics, componentIdForMetrics)

	r.WaitForCacheUpdate(ctx, req.NamespacedName, &component)

	return ctrl.Result{}, nil
}

func (r *ComponentBuildReconciler) WaitForCacheUpdate(ctx context.Context, namespace types.NamespacedName, component *appstudiov1alpha1.Component) {
	log := ctrllog.FromContext(ctx)

	// Here we do some trick.
	// The problem is that the component update triggers both: a new reconcile and operator cache update.
	// In other words we are getting race condition. If a new reconcile is triggered before cache update,
	// requested build action will be repeated, because the last update has not yet visible for the operator.
	// For example, instead of one initial pipeline run we could get two.
	// To resolve the problem above, instead of just ending the reconcile loop here,
	// we are waiting for the cache update. This approach prevents next reconciles with outdated cache.
	isComponentInCacheUpToDate := false
	for i := 0; i < 20; i++ {
		if err := r.Client.Get(ctx, namespace, component); err == nil {
			_, buildRequestAnnotationExists := component.Annotations[BuildRequestAnnotationName]
			_, buildStatusAnnotationExists := component.Annotations[BuildStatusAnnotationName]
			if !buildRequestAnnotationExists && buildStatusAnnotationExists {
				// Cache contains updated component
				isComponentInCacheUpToDate = true
				break
			}
			// Outdated version of the component, wait more.
		} else {
			if errors.IsNotFound(err) {
				// The component was deleted
				isComponentInCacheUpToDate = true
				break
			}
			log.Error(err, "failed to get the component", l.Action, l.ActionView)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !isComponentInCacheUpToDate {
		log.Info("failed to wait for updated cache. Requested action could be repeated.", l.Audit, "true")
	}
}

func readBuildStatus(component *appstudiov1alpha1.Component) *BuildStatus {
	if component.Annotations == nil {
		return &BuildStatus{}
	}

	buildStatus := &BuildStatus{}
	buildStatusBytes := []byte(component.Annotations[BuildStatusAnnotationName])
	if err := json.Unmarshal(buildStatusBytes, buildStatus); err == nil {
		return buildStatus
	}
	return &BuildStatus{}
}

func writeBuildStatus(component *appstudiov1alpha1.Component, buildStatus *BuildStatus) {
	if component.Annotations == nil {
		component.Annotations = make(map[string]string)
	}

	buildStatusBytes, _ := json.Marshal(buildStatus)
	component.Annotations[BuildStatusAnnotationName] = string(buildStatusBytes)
}
