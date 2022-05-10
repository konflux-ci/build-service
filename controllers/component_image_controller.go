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
	ComponentTaskLabelName = "build.appstudio.openshift.io/component"
	BuildTaskName          = "build-container"
)

// NewComponentImageReconciler reconciles a Frigate object
type NewComponentImageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

// SetupWithManager sets up the controller with the Manager.
func (r *NewComponentImageReconciler) SetupWithManager(mgr ctrl.Manager) error {
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *NewComponentImageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("NewComponentImage", req.NamespacedName)

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

	owners := pipelineRun.GetOwnerReferences()
	if len(owners) == 0 {
		log.Info(fmt.Sprintf("PipelineRun '%v' has no owner reference", req.NamespacedName))
		return ctrl.Result{}, nil
	}
	owner := owners[0]

	// Get associated Component
	var component appstudiov1alpha1.Component
	componentKey := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      owner.Name,
	}
	err = r.Client.Get(ctx, componentKey, &component)
	if err != nil {
		if errors.IsNotFound(err) {
			// Component has been deleted, nothing to do.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Obtain newly built image

	// Search for build TaskRun
	componentTaskRunRequirement, err := labels.NewRequirement(ComponentTaskLabelName, selection.Equals, []string{component.Name})
	if err != nil {
		return ctrl.Result{}, err
	}
	componentTaskRunSelector := labels.NewSelector().Add(*componentTaskRunRequirement)
	var tasks tektonapi.TaskRunList
	if err := r.Client.List(ctx, &tasks, &client.ListOptions{LabelSelector: componentTaskRunSelector}); err != nil {
		return ctrl.Result{}, err
	}
	var taskrun *tektonapi.TaskRun
	for _, tr := range tasks.Items {
		if strings.Contains(tr.Name, BuildTaskName) {
			taskrun = &tr
			break
		}
	}
	if taskrun == nil {
		// Build task not found, shouldn't happen
		err := fmt.Errorf("failed to find build TaskRun for %v pipeline", req.NamespacedName)
		log.Info(err.Error())
		return ctrl.Result{}, err
	}

	imageReference := ""
	for _, taskRunResult := range taskrun.Status.TaskRunResults {
		if taskRunResult.Name == "IMAGE_URL" {
			imageReference = taskRunResult.Value
			break
		}
	}
	if imageReference == "" {
		err := fmt.Errorf("failed to find build image parameter in TaskRun for %v pipeline", req.NamespacedName)
		log.Info(err.Error())
		return ctrl.Result{}, err
	}

	// Update the image reference in the component
	if component.Spec.Build.ContainerImage != imageReference {
		component.Spec.Build.ContainerImage = imageReference
		if err := r.Client.Update(ctx, &component); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}
