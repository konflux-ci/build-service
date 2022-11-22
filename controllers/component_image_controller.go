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
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"knative.dev/pkg/apis"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"
	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
)

const (
	ComponentNameLabelName        = "appstudio.openshift.io/component"
	ApplicationNameLabelName      = "appstudio.openshift.io/application"
	PipelineRunLabelName          = "tekton.dev/pipelineRun"
	PipelineTaskLabelName         = "tekton.dev/pipelineTask"
	UpdateComponentAnnotationName = "appstudio.redhat.com/updateComponentOnSuccess"
	BuildImageTaskName            = "build-container"
	PullRequestAnnotationName     = "pipelinesascode.tekton.dev/pull-request"

	k8sNameMaxLen = 253
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
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=applications,verbs=get;list;watch;
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=applications/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=snapshots,verbs=create
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
	imageReference, err := getImageReferenceFromBuildPipeline(pipelineRun)
	if err != nil {
		// Build pipeline finished successfully, but information about new image is missing.
		// This could happen when image is already built and the build step is skipped.
		// There is no point in retrying, because finished pipeline doesn't have results set.
		log.Info("No new image found in build PipelineRun")
		return ctrl.Result{}, nil
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

	// Create Application Snapshot
	// This object will be used to regenerate the GitOps repository
	// (based on the ApplicationSnapshotEnvironmentBinding, which references the ApplicationSnapshot)
	applicationName := component.Spec.Application

	application := &appstudiov1alpha1.Application{}
	applicationKey := types.NamespacedName{Name: applicationName, Namespace: req.Namespace}
	if err := r.Client.Get(ctx, applicationKey, application); err != nil {
		log.Error(err, fmt.Sprintf("Failed to get application \"%s\"", applicationName))
		return ctrl.Result{}, err
	}

	applicationComponents, err := r.getApplicationComponents(ctx, application)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to list components of application \"%s\"", applicationName))
		return ctrl.Result{}, err
	}

	applicationSnapshot, err := generateApplicationSnapshot(application, applicationComponents)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to generate application snapshot \"%s\"", applicationName))
		return ctrl.Result{}, err
	}

	if err := r.Client.Create(ctx, applicationSnapshot); err != nil {
		log.Error(err, fmt.Sprintf("Failed to create application snapshot \"%s\"", applicationName))
		return ctrl.Result{}, err
	}
	log.Info(fmt.Sprintf("Created application snapshot %s", applicationSnapshot.GetName()))

	return ctrl.Result{}, nil
}

func getImageReferenceFromBuildPipeline(buildPipelineRun tektonapi.PipelineRun) (string, error) {
	pipelineResults := buildPipelineRun.Status.PipelineResults
	if pipelineResults == nil {
		return "", fmt.Errorf("PipelineRun.Status.PipelineResults is empty")
	}

	imageUrl := ""
	for _, pipelineRunResult := range pipelineResults {
		if pipelineRunResult.Name == "IMAGE_URL" {
			imageUrl = pipelineRunResult.Value
		}
	}
	if imageUrl == "" {
		return "", fmt.Errorf("IMAGE_URL is empty in build pipeline result")
	}

	imageDigest := ""
	for _, taskResult := range pipelineResults {
		if taskResult.Name == "IMAGE_DIGEST" {
			imageDigest = taskResult.Value
		}
	}

	imageReference := imageUrl
	if imageDigest != "" {
		if strings.Contains(imageUrl, ":") {
			// There is tag present, remove it
			imageUrl = strings.Split(imageUrl, ":")[0]
		}
		imageReference = imageUrl + "@" + imageDigest
	}

	return imageReference, nil
}

func (r *ComponentImageReconciler) getApplicationComponents(ctx context.Context, application *appstudiov1alpha1.Application) ([]appstudiov1alpha1.Component, error) {
	applicationName := application.GetName()

	var allComponentsList appstudiov1alpha1.ComponentList
	listOptions := &client.ListOptions{Namespace: application.GetNamespace()}
	if err := r.Client.List(ctx, &allComponentsList, listOptions); err != nil {
		return nil, err
	}

	var applicationComponents []appstudiov1alpha1.Component
	for _, component := range allComponentsList.Items {
		if component.Spec.Application == applicationName {
			applicationComponents = append(applicationComponents, component)
		}
	}
	if len(applicationComponents) < 1 {
		return nil, fmt.Errorf("application \"%s\" has no components", applicationName)
	}

	return applicationComponents, nil
}

func generateApplicationSnapshot(application *appstudiov1alpha1.Application, components []appstudiov1alpha1.Component) (*appstudiov1alpha1.Snapshot, error) {
	if len(components) < 1 {
		return nil, fmt.Errorf("application must contain at least one component")
	}

	var imagesList []string
	var applicationSnapshotComponents []appstudiov1alpha1.SnapshotComponent
	for _, component := range components {
		applicationSnapshotComponents = append(applicationSnapshotComponents, appstudiov1alpha1.SnapshotComponent{
			Name:           component.GetName(),
			ContainerImage: component.Spec.ContainerImage,
		})

		imagesList = append(imagesList, component.GetName()+"->"+component.Spec.ContainerImage)
	}

	applicationName := application.GetName()
	snapshotName := getApplicationSnapshotName(applicationName)
	displayName := fmt.Sprintf("Snapshot of \"%s\" application", applicationName)
	displayDescription := fmt.Sprintf("Snapshot of \"%s\" application created at %s. Component images: %s",
		applicationName, time.Now().Format("2006 Jan 02 Monday 15:04:05"), strings.Join(imagesList, " "))

	applicationSnapshot := &appstudiov1alpha1.Snapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshotName,
			Namespace: application.GetNamespace(),
			Labels: map[string]string{
				ApplicationNameLabelName: applicationName,
			},
		},
		Spec: appstudiov1alpha1.SnapshotSpec{
			Application:        applicationName,
			DisplayName:        displayName,
			DisplayDescription: displayDescription,
			Components:         applicationSnapshotComponents,
		},
	}

	applicationSnapshot.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: application.APIVersion,
			Kind:       application.Kind,
			Name:       application.Name,
			UID:        application.UID,
		},
	})

	return applicationSnapshot, nil
}

func getApplicationSnapshotName(applicationName string) string {
	timestamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	name := fmt.Sprintf("snapshot-of-application-%s-%s", applicationName, timestamp)
	if len(name) <= k8sNameMaxLen {
		return name
	}

	// Need to truncate application name
	applicationNameAllowedLen := len(applicationName) - (len(name) - k8sNameMaxLen)
	return getApplicationSnapshotName(applicationName[:applicationNameAllowedLen])
}
