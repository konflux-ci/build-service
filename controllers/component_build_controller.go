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
	"fmt"

	corev1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/prometheus/client_golang/prometheus"
	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/build-service/pkg/boerrors"
	l "github.com/redhat-appstudio/build-service/pkg/logs"
)

const (
	InitialBuildAnnotationName = "appstudio.openshift.io/component-initial-build"

	PaCProvisionFinalizer            = "pac.component.appstudio.openshift.io/finalizer"
	ImageRegistrySecretLinkFinalizer = "image-registry-secret-sa-link.component.appstudio.openshift.io/finalizer"

	PaCProvisionAnnotationName             = "appstudio.openshift.io/pac-provision"
	PaCProvisionRequestedAnnotationValue   = "request"
	PaCProvisionDoneAnnotationValue        = "done"
	PaCProvisionErrorAnnotationValue       = "error"
	PaCProvisionErrorDetailsAnnotationName = "appstudio.openshift.io/pac-provision-error"

	ApplicationNameLabelName  = "appstudio.openshift.io/application"
	ComponentNameLabelName    = "appstudio.openshift.io/component"
	PartOfLabelName           = "app.kubernetes.io/part-of"
	PartOfAppStudioLabelValue = "appstudio"

	gitCommitShaAnnotationName    = "build.appstudio.redhat.com/commit_sha"
	gitRepoAtShaAnnotationName    = "build.appstudio.openshift.io/repo"
	gitTargetBranchAnnotationName = "build.appstudio.redhat.com/target_branch"

	ImageRepoAnnotationName         = "image.redhat.com/image"
	ImageRepoGenerateAnnotationName = "image.redhat.com/generate"
	buildPipelineServiceAccountName = "appstudio-pipeline"

	buildServiceNamespaceName         = "build-service"
	buildPipelineSelectorResourceName = "build-pipeline-selector"

	metricsNamespace = "redhat_appstudio"
	metricsSubsystem = "buildservice"
)

var (
	simpleBuildPipelineCreationTimeMetric       prometheus.Histogram
	pipelinesAsCodeComponentProvisionTimeMetric prometheus.Histogram
)

func initMetrics() error {
	buckets := getProvisionTimeMetricsBuckets()

	simpleBuildPipelineCreationTimeMetric = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Buckets:   buckets,
		Name:      "initial_build_pipeline_creation_time",
		Help:      "The time in seconds spent from the moment of Component creation till the initial build pipeline submission.",
	})
	pipelinesAsCodeComponentProvisionTimeMetric = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Buckets:   buckets,
		Name:      "PaC_configuration_time",
		Help:      "The time in seconds spent from the moment of Component creation till Pipelines-as-Code configuration done in the Component source repository.",
	})

	if err := metrics.Registry.Register(simpleBuildPipelineCreationTimeMetric); err != nil {
		return fmt.Errorf("failed to register the initial_build_pipeline_creation_time metric: %w", err)
	}
	if err := metrics.Registry.Register(pipelinesAsCodeComponentProvisionTimeMetric); err != nil {
		return fmt.Errorf("failed to register the PaC_configuration_time metric: %w", err)
	}

	return nil
}

func getProvisionTimeMetricsBuckets() []float64 {
	return []float64{5, 10, 15, 20, 30, 60, 120, 300}
}

// ComponentBuildReconciler watches AppStudio Component objects in order to
// provision Pipelines as Code configuration for the Component or
// submit initial builds and dependent resources if PaC is not configured.
type ComponentBuildReconciler struct {
	Client        client.Client
	Scheme        *runtime.Scheme
	EventRecorder record.EventRecorder
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := initMetrics(); err != nil {
		return err
	}

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
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=buildpipelineselectors,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=create
//+kubebuilder:rbac:groups=pipelinesascode.tekton.dev,resources=repositories,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;patch;update
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch

func (r *ComponentBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("ComponentOnboarding")
	ctx = ctrllog.IntoContext(ctx, log)

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

	if getContainerImageRepositoryForComponent(&component) == "" {
		// Container image must be set. It's not possible to proceed without it.
		log.Info("Waiting for ContainerImage to be set")
		return ctrl.Result{}, nil
	}

	// Do not run any builds for any container-image components
	if component.Spec.ContainerImage != "" && (component.Spec.Source.GitSource == nil || component.Spec.Source.GitSource.URL == "") {
		log.Info("Nothing to do for container image component")
		return ctrl.Result{}, nil
	}

	if !component.ObjectMeta.DeletionTimestamp.IsZero() {
		// Deletion of the component is requested

		if controllerutil.ContainsFinalizer(&component, ImageRegistrySecretLinkFinalizer) {
			pipelineSA := &corev1.ServiceAccount{}
			err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: req.Namespace}, pipelineSA)
			if err != nil && !errors.IsNotFound(err) {
				log.Error(err, fmt.Sprintf("Failed to read service account %s in namespace %s", buildPipelineServiceAccountName, req.Namespace), l.Action, l.ActionView)
				return ctrl.Result{}, err
			}
			if err == nil { // If pipeline service account found, unlink the secret from it
				if _, generatedImageRepoSecretName, err := getComponentImageRepoAndSecretNameFromImageAnnotation(&component); err == nil {
					if _, err := r.unlinkSecretFromServiceAccount(ctx, generatedImageRepoSecretName, pipelineSA.Name, pipelineSA.Namespace); err != nil {
						return ctrl.Result{}, err
					}
					// unlink secret also to old pipeline account, can be removed when default pipeline is switched to appstudio-pipeline
					_, _ = r.unlinkSecretFromServiceAccount(ctx, generatedImageRepoSecretName, "pipeline", pipelineSA.Namespace)
				}
			}

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

		if controllerutil.ContainsFinalizer(&component, PaCProvisionFinalizer) {
			// In order not to block the deletion of the Component delete finalizer
			// and then try to do clean up ignoring errors.

			// Delete Pipelines as Code provision finalizer
			controllerutil.RemoveFinalizer(&component, PaCProvisionFinalizer)
			if err := r.Client.Update(ctx, &component); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("PaC finalizer removed", l.Action, l.ActionDelete)

			// Try to clean up Pipelines as Code configuration
			r.UndoPaCProvisionForComponent(ctx, &component)
		}

		return ctrl.Result{}, nil
	}

	// Ensure devfile model is set
	if component.Status.Devfile == "" {
		// The Component has been just created.
		// Component controller (from Application Service) must set devfile model, wait for it.
		log.Info("Waiting for devfile model in component")
		// Do not requeue as after model update a new update event will trigger a new reconcile
		return ctrl.Result{}, nil
	}

	// Ensure pipeline service account exists
	pipelineSA, err := r.ensurePipelineServiceAccount(ctx, component.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Link auto generated image registry secret in case of auto generated image repository is used.
	isSwitchedImageRegistry := false
	if !controllerutil.ContainsFinalizer(&component, ImageRegistrySecretLinkFinalizer) {
		imageRepoGenerated, imageRepoSecretName, err := getComponentImageRepoAndSecretNameFromImageAnnotation(&component)
		if err != nil {
			return ctrl.Result{}, err
		}

		// Check if the generated image is used
		if imageRepoGenerated != "" && (component.Spec.ContainerImage == "" || imageRepoGenerated == getContainerImageRepository(component.Spec.ContainerImage)) {
			_, err = r.linkSecretToServiceAccount(ctx, imageRepoSecretName, pipelineSA.Name, pipelineSA.Namespace, true)
			if err != nil {
				return ctrl.Result{}, err
			}
			// link secret also to old pipeline account, can be removed when default pipeline is switched to appstudio-pipeline
			_, _ = r.linkSecretToServiceAccount(ctx, imageRepoSecretName, "pipeline", pipelineSA.Namespace, true)

			// Ensure finalizer exists to clean up image registry secret link on component deletion
			if component.ObjectMeta.DeletionTimestamp.IsZero() {
				if !controllerutil.ContainsFinalizer(&component, ImageRegistrySecretLinkFinalizer) {
					if err := r.Client.Get(ctx, req.NamespacedName, &component); err != nil {
						log.Error(err, "failed to get Component", l.Action, l.ActionView)
						return ctrl.Result{}, err
					}
					controllerutil.AddFinalizer(&component, ImageRegistrySecretLinkFinalizer)
					if err := r.Client.Update(ctx, &component); err != nil {
						return ctrl.Result{}, err
					}
					isSwitchedImageRegistry = true
					log.Info("Image registry secret service account link finalizer added", l.Action, l.ActionUpdate)
				}
			}
		}
	}

	// Check if Pipelines as Code workflow enabled
	if val, exists := component.Annotations[PaCProvisionAnnotationName]; exists {
		if val != PaCProvisionRequestedAnnotationValue && !isSwitchedImageRegistry {
			if !(val == PaCProvisionDoneAnnotationValue || val == PaCProvisionErrorAnnotationValue) {
				message := fmt.Sprintf(
					"Unexpected value \"%s\" for \"%s\" annotation. Use \"%s\" value to do Pipeline as Code provision for the Component",
					val, PaCProvisionAnnotationName, PaCProvisionRequestedAnnotationValue)
				log.Info(message)
			}
			// Nothing to do
			return ctrl.Result{}, nil
		}

		log.Info("Starting Pipelines as Code provision for the Component")

		var pacAnnotationValue string
		var pacPersistentErrorMessage string
		err := r.ProvisionPaCForComponent(ctx, &component)
		if err != nil {
			if boErr, ok := err.(*boerrors.BuildOpError); ok && boErr.IsPersistent() {
				log.Error(err, "Pipelines as Code provision for the Component failed")
				pacAnnotationValue = PaCProvisionErrorAnnotationValue
				pacPersistentErrorMessage = boErr.ShortError()
			} else {
				// transient error, retry
				log.Error(err, "Pipelines as Code provision transient error")
				return ctrl.Result{}, err
			}
		} else {
			pacAnnotationValue = PaCProvisionDoneAnnotationValue
			log.Info("Pipelines as Code provision for the Component finished successfully")
		}

		// Update component to show Pipeline as Code provision is done
		if err := r.Client.Get(ctx, req.NamespacedName, &component); err != nil {
			log.Error(err, "failed to get Component", l.Action, l.ActionView)
			return ctrl.Result{}, err
		}

		// Update PaC annotation
		if len(component.Annotations) == 0 {
			component.Annotations = make(map[string]string)
		}
		component.Annotations[PaCProvisionAnnotationName] = pacAnnotationValue
		if pacPersistentErrorMessage != "" {
			component.Annotations[PaCProvisionErrorDetailsAnnotationName] = pacPersistentErrorMessage
		} else {
			delete(component.Annotations, PaCProvisionErrorDetailsAnnotationName)
		}

		// Add finalizer to clean up Pipelines as Code configuration on component deletion
		if component.ObjectMeta.DeletionTimestamp.IsZero() {
			if !controllerutil.ContainsFinalizer(&component, PaCProvisionFinalizer) {
				controllerutil.AddFinalizer(&component, PaCProvisionFinalizer)
			}
		}

		if err := r.Client.Update(ctx, &component); err != nil {
			log.Error(err, "failed to add PaC finalizer to the Component", l.Action, l.ActionUpdate, l.Audit, "true")
			return ctrl.Result{}, err
		} else {
			log.Info("PaC finalizer added", l.Action, l.ActionUpdate)
		}

		return ctrl.Result{}, nil
	}

	// Pipelines as Code workflow is not enabled, use plain builds.

	// Reread component to avoid out of date state
	if err := r.Client.Get(ctx, req.NamespacedName, &component); err != nil {
		log.Error(err, "failed to get Component", l.Action, l.ActionView)
		return ctrl.Result{}, err
	}

	// Check initial build annotation to know if any work should be done for the Component
	if len(component.Annotations) == 0 {
		component.Annotations = make(map[string]string)
	}
	if _, exists := component.Annotations[InitialBuildAnnotationName]; exists {
		// Initial build have already happend, nothing to do.
		return ctrl.Result{}, nil
	}
	// The initial build is needed for the Component

	// Set initial build annotation to prevent next builds
	component.Annotations[InitialBuildAnnotationName] = "processed"
	if err := r.Client.Update(ctx, &component); err != nil {
		log.Error(err, "failed to add initial build annotation to the Component", l.Action, l.ActionUpdate)
		return ctrl.Result{}, err
	}

	if err := r.SubmitNewBuild(ctx, &component); err != nil {
		// Try to revert the initial build annotation
		if err := r.Client.Get(ctx, req.NamespacedName, &component); err == nil {
			if len(component.Annotations) > 0 {
				delete(component.Annotations, InitialBuildAnnotationName)
				if err := r.Client.Update(ctx, &component); err != nil {
					log.Error(err, "failed to reschedule initial build for the Component", l.Action, l.ActionUpdate)
					return ctrl.Result{}, err
				}
			}
		} else {
			log.Error(err, "failed to reschedule initial build for the Component", l.Action, l.ActionView)
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}
