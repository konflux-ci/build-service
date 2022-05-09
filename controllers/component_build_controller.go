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
	"net/url"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	"github.com/redhat-appstudio/application-service/gitops/prepare"
)

const (
	InitialBuildAnnotationName = "com.redhat.appstudio/component-initial-build-happend"
)

// ComponentBuildReconciler watches AppStudio Component object in order to submit builds
type ComponentBuildReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
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
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *ComponentBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ComponentInitialBuild", req.NamespacedName)

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

	// Do not run any builds for any container-image components
	if component.Spec.Source.ImageSource != nil && component.Spec.Source.ImageSource.ContainerImage != "" {
		log.Info(fmt.Sprintf("Nothing to do for container image component: %v", req.NamespacedName))
		return ctrl.Result{}, nil
	}

	if component.Status.Devfile == "" {
		// The component has been just created.
		// Component controller must set devfile model, wait for it.
		log.Info(fmt.Sprintf("Waiting for devfile model in component: %v", req.NamespacedName))
		// Do not requeue as after model update a new update event will trigger a new reconcile
		return ctrl.Result{}, nil
	}

	if len(component.Annotations) == 0 {
		component.Annotations = make(map[string]string)
	}
	if component.Annotations[InitialBuildAnnotationName] == "true" {
		// Initial build have already happend, nothing to do.
		return ctrl.Result{}, nil
	}

	// Set initial build annotation to prevent next builds
	component.Annotations[InitialBuildAnnotationName] = "true"
	if err := r.Client.Update(ctx, &component); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.SubmitNewBuild(ctx, component); err != nil {
		// Try to revert the annotation
		if err := r.Client.Get(ctx, req.NamespacedName, &component); err == nil {
			component.Annotations[InitialBuildAnnotationName] = "false"
			if err := r.Client.Update(ctx, &component); err != nil {
				log.Error(err, fmt.Sprintf("Failed to schedule initial build for component: %v", req.NamespacedName))
			}
		} else {
			log.Error(err, fmt.Sprintf("Failed to schedule initial build for component: %v", req.NamespacedName))
		}

		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SubmitNewBuild creates a new PipelineRun to build a new image for the given component.
func (r *ComponentBuildReconciler) SubmitNewBuild(ctx context.Context, component appstudiov1alpha1.Component) error {
	log := r.Log.WithValues("Namespace", component.Namespace, "Application", component.Spec.Application, "Component", component.Name)

	// TODO delete this block which is workaround for delayed sync of pvc
	workspaceStorage := gitops.GenerateCommonStorage(component, "appstudio")
	existingPvc := &corev1.PersistentVolumeClaim{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: workspaceStorage.Name, Namespace: workspaceStorage.Namespace}, existingPvc); err != nil {
		if errors.IsNotFound(err) {
			// Patch PVC size to 1 Gi, because default 10 Mi is not enough
			workspaceStorage.Spec.Resources.Requests["storage"] = resource.MustParse("1Gi")
			// Create PVC (Argo CD will patch it later)
			err = r.Client.Create(ctx, workspaceStorage)
			if err != nil {
				log.Error(err, fmt.Sprintf("Unable to create common storage %v", workspaceStorage))
				return err
			}
			log.Info(fmt.Sprintf("PV is now present : %v", workspaceStorage.Name))
		} else {
			log.Error(err, fmt.Sprintf("Unable to get common storage %v", workspaceStorage))
			return err
		}
	}

	gitSecretName := component.Spec.Secret
	// Make the Secret ready for consumption by Tekton.
	if gitSecretName != "" {
		gitSecret := corev1.Secret{}
		err := r.Client.Get(ctx, types.NamespacedName{Name: gitSecretName, Namespace: component.Namespace}, &gitSecret)
		if err != nil {
			log.Error(err, fmt.Sprintf("Secret %s is missing", gitSecretName))
			return err
		} else {
			if gitSecret.Annotations == nil {
				gitSecret.Annotations = map[string]string{}
			}

			gitHost, _ := getGitProvider(component.Spec.Source.GitSource.URL)

			// Doesn't matter if it was present, we will always override.
			gitSecret.Annotations["tekton.dev/git-0"] = gitHost
			err = r.Client.Update(ctx, &gitSecret)
			if err != nil {
				log.Error(err, fmt.Sprintf("Secret %s update failed", gitSecretName))
				return err
			}
		}
	}

	pipelinesServiceAccount := corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: "pipeline", Namespace: component.Namespace}, &pipelinesServiceAccount)
	if err != nil {
		log.Error(err, fmt.Sprintf("OpenShift Pipelines-created Service account 'pipeline' is missing in namespace %s", component.Namespace))
		return err
	} else {
		updateRequired := updateServiceAccountIfSecretNotLinked(gitSecretName, &pipelinesServiceAccount)
		if updateRequired {
			err = r.Client.Update(ctx, &pipelinesServiceAccount)
			if err != nil {
				log.Error(err, fmt.Sprintf("Unable to update pipeline service account %v", pipelinesServiceAccount))
				return err
			}
			log.Info(fmt.Sprintf("Service Account updated %v", pipelinesServiceAccount))
		}
	}

	gitopsConfig := prepare.PrepareGitopsConfig(ctx, r.Client, component)
	initialBuild := gitops.GenerateInitialBuildPipelineRun(component, gitopsConfig)
	err = controllerutil.SetOwnerReference(&component, &initialBuild, r.Scheme)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to set owner reference for %v", initialBuild))
	}
	err = r.Client.Create(ctx, &initialBuild)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to create the build PipelineRun %v", initialBuild))
		return err
	}
	log.Info(fmt.Sprintf("Initial build pipeline created for component %s in %s namespace", component.Name, component.Namespace))

	return nil
}

// getGitProvider takes a Git URL of the format https://github.com/foo/bar and returns https://github.com
func getGitProvider(gitURL string) (string, error) {
	u, err := url.Parse(gitURL)

	// We really need the format of the string to be correct.
	// We'll not do any autocorrection.
	if err != nil || u.Scheme == "" {
		return "", fmt.Errorf("failed to parse string into a URL: %v or scheme is empty", err)
	}
	return u.Scheme + "://" + u.Host, nil
}

func updateServiceAccountIfSecretNotLinked(gitSecretName string, serviceAccount *corev1.ServiceAccount) bool {
	for _, credentialSecret := range serviceAccount.Secrets {
		if credentialSecret.Name == gitSecretName {
			// The secret is present in the service account, no updates needed
			return false
		}
	}

	// Add the secret to secret account and return that update is needed
	serviceAccount.Secrets = append(serviceAccount.Secrets, corev1.ObjectReference{Name: gitSecretName})
	return true
}
