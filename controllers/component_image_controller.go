/*
Copyright 2022.

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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"knative.dev/pkg/apis"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"
	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
)

const (
	ComponentNameLabelName        = "build.appstudio.openshift.io/component"
	PipelineRunLabelName          = "tekton.dev/pipelineRun"
	PipelineTaskLabelName         = "tekton.dev/pipelineTask"
	UpdateComponentAnnotationName = "appstudio.redhat.com/updateComponentOnSuccess"
	BuildImageTaskName            = "build-container"
	PullRequestAnnotationName     = "pipelinesascode.tekton.dev/pull-request"
)

// ComponentImageReconciler reconciles a Frigate object
type ComponentImageReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Log           logr.Logger
	EventRecorder record.EventRecorder
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentImageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tektonapi.PipelineRun{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return false
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// React only if pipeline build has just finished successfully and a new image is ready

				var old *tektonapi.PipelineRun
				var new *tektonapi.PipelineRun
				old, ok := e.ObjectOld.(*tektonapi.PipelineRun)
				if !ok {
					return false
				}
				new, ok = e.ObjectNew.(*tektonapi.PipelineRun)
				if !ok {
					return false
				}

				if new.Status.CompletionTime == nil || new.Status.CompletionTime.IsZero() {
					// Pipeline run is still in progress
					return false
				}

				if old.Status.CompletionTime != nil {
					// Build had been finished before this update
					return false
				}

				// Ensure the PipelineRun belongs to a Component
				if new.ObjectMeta.Labels == nil || new.ObjectMeta.Labels[ComponentNameLabelName] == "" {
					// PipelineRun does not belong to a Component
					return false
				}

				// Check if the build should be handled by the operator
				if new.ObjectMeta.Annotations != nil && new.ObjectMeta.Annotations[UpdateComponentAnnotationName] == "false" {
					return false
				}

				// Skip build on PullRequest
				if new.ObjectMeta.Annotations != nil && new.ObjectMeta.Annotations[PullRequestAnnotationName] != "" {
					return false
				}

				// Ensure the build is successful
				buildSuccessful := false
				for _, c := range new.Status.Conditions {
					if c.Type == apis.ConditionSucceeded && c.Status == "True" {
						buildSuccessful = true
						break
					}
				}
				if !buildSuccessful {
					return false
				}

				// Ensure the build used to be in progress
				buildWasInProgress := false
				for _, c := range old.Status.Conditions {
					if c.Type == apis.ConditionSucceeded && c.Reason == "Running" {
						buildWasInProgress = true
						break
					}
				}
				return buildWasInProgress
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		})).
		Complete(r)
}

//+kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=get;list;watch
//+kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=tekton.dev,resources=taskruns,verbs=get;list;watch
//+kubebuilder:rbac:groups=tekton.dev,resources=taskruns/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components/status,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *ComponentImageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("PipelineRun", req.NamespacedName)

	log.Info("PipelineRun succeeded, checking for a new image...")

	// Fetch the PipelineRun
	var pipelineRun tektonapi.PipelineRun
	err := r.Client.Get(ctx, req.NamespacedName, &pipelineRun)
	if err != nil {
		if errors.IsNotFound(err) {
			// PipelineRun has been deleted, nothing to do.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Get Component the PipelineRun is associated to.
	if pipelineRun.ObjectMeta.Labels == nil || len(pipelineRun.ObjectMeta.Labels[ComponentNameLabelName]) == 0 {
		log.Info(fmt.Sprintf("PipelineRun '%v' has no '%s' label", req.NamespacedName, ComponentNameLabelName))
		// Failed to detect Component owner, stop.
		return ctrl.Result{}, nil
	}
	componentName := pipelineRun.ObjectMeta.Labels[ComponentNameLabelName]

	var component appstudiov1alpha1.Component
	componentKey := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      componentName,
	}
	err = r.Client.Get(ctx, componentKey, &component)
	if err != nil {
		if errors.IsNotFound(err) {
			// Component has been deleted, nothing to do.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, fmt.Sprintf("Failed to read component %v", componentKey))
		return ctrl.Result{}, err
	}

	// Obtain newly built image

	// Search for the build TaskRun
	taskRunBelongsToPipelineRunRequirement, err := labels.NewRequirement(PipelineRunLabelName, selection.Equals, []string{pipelineRun.Name})
	if err != nil {
		return ctrl.Result{}, err
	}
	taskRunPipelineTaskNameRequirement, err := labels.NewRequirement(PipelineTaskLabelName, selection.Equals, []string{BuildImageTaskName})
	if err != nil {
		return ctrl.Result{}, err
	}
	componentBuildTaskRunSelector := labels.NewSelector().Add(*taskRunBelongsToPipelineRunRequirement).Add(*taskRunPipelineTaskNameRequirement)
	var tasks tektonapi.TaskRunList
	if err := r.Client.List(ctx, &tasks, &client.ListOptions{LabelSelector: componentBuildTaskRunSelector}); err != nil {
		return ctrl.Result{}, err
	}

	var taskrun *tektonapi.TaskRun
	switch len(tasks.Items) {
	case 0:
		// Build task not found, it was skipped
		log.Info(fmt.Sprintf("No new image built in '%v' PipelineRun for '%v' Comoponent", req.NamespacedName, componentKey))
		return ctrl.Result{}, nil
	case 1:
		taskrun = &tasks.Items[0]
	default:
		// Should not happen
		message := fmt.Sprintf("Found %d build tasks for %v PipelineRun", len(tasks.Items), pipelineRun)
		log.Info(message)
		r.EventRecorder.Event(&pipelineRun, "Warning", "TooManyBuildTasksForPipeline", message)
		return ctrl.Result{}, nil
	}

	imageReference := ""
	for _, taskRunResult := range taskrun.Status.TaskRunResults {
		if taskRunResult.Name == "IMAGE_URL" {
			imageReference = strings.TrimSpace(taskRunResult.Value)
			break
		}
	}
	if imageReference == "" {
		err := fmt.Errorf("failed to find build image in build TaskRun status of %v pipeline", req.NamespacedName)
		log.Info(err.Error())
		return ctrl.Result{}, err
	}

	// Update the image reference in the component
	if component.Spec.ContainerImage != imageReference {
		component.Spec.ContainerImage = imageReference
		if err := r.Client.Update(ctx, &component); err != nil {
			return ctrl.Result{}, err
		}
		log.Info(fmt.Sprintf("Updated '%v' component image to '%s'", componentKey, imageReference))
	} else {
		log.Info(fmt.Sprintf("Component '%v' image '%s' is up to date", componentKey, imageReference))
	}

	return ctrl.Result{}, nil
}
