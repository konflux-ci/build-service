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
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	routev1 "github.com/openshift/api/route/v1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	triggersapi "github.com/tektoncd/triggers/pkg/apis/triggers/v1alpha1"
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
	"github.com/redhat-appstudio/build-service/pkg/gitops"
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *ComponentBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ComponentBuilder", req.NamespacedName)

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

	if component.Status.Devfile == "" {
		// The component has been just created.
		// Component controller must set devfile model, wait for it.
		log.Info(fmt.Sprintf("Waiting for devfile model in component: %v", req.NamespacedName))
		// Do not requeue as after model update a new update event will trigger a new reconcile
		return ctrl.Result{}, nil
	}

	shouldBuild, err := r.IsNewBuildRequired(ctx, component)
	if err != nil {
		return ctrl.Result{}, err
	}
	if shouldBuild {
		if err := r.SubmitNewBuild(ctx, component); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

var triggerTemplateDiffOpts = cmp.Options{
	cmpopts.IgnoreFields(triggersapi.TriggerTemplate{}, "TypeMeta", "ObjectMeta"),
	cmpopts.IgnoreFields(runtime.RawExtension{}, "Raw"),
	cmp.Comparer(func(x, y resource.Quantity) bool {
		return x.Cmp(y) == 0
	}),
}
var triggerResourceTemplateDiffOpts = cmp.Options{
	cmp.Comparer(func(x, y resource.Quantity) bool {
		return x.Cmp(y) == 0
	}),
}

// IsNewBuildRequired detects if a new image should be built for given component.
// The criterion is equality of existing and expected trigger template of the component.
func (r *ComponentBuildReconciler) IsNewBuildRequired(ctx context.Context, component appstudiov1alpha1.Component) (bool, error) {
	log := r.Log.WithValues("Namespace", component.Namespace, "Application", component.Spec.Application, "Component", component.Name)

	expectedTriggerTemplate, err := gitops.GenerateTriggerTemplate(component)
	if err != nil {
		return false, err
	}

	// Get existing build trigger template, if any
	existingTriggerTemplate := &triggersapi.TriggerTemplate{}
	existingTriggerTemplateNamespacedName := types.NamespacedName{
		Name:      expectedTriggerTemplate.Name,
		Namespace: expectedTriggerTemplate.Namespace,
	}
	if err := r.Client.Get(ctx, existingTriggerTemplateNamespacedName, existingTriggerTemplate); err != nil {
		if errors.IsNotFound(err) {
			// Build has never been done or cleaned up. Rebuild.
			log.Info("Previous trigger template not found, rebuild.")
			return true, nil
		}
		return false, err
	}

	// Compare expectedTriggerTemplate and existingTriggerTemplate.
	// The difficulty here is that we cannot just compare these objects using DeepEqual or similar,
	// because generated (expected) TriggerTemplate has TriggerResourceTemplate stored in raw format.
	// The raw TriggerResourceTemplate is expanded at runtime.
	// We cannnot compare raw (string) values of TriggerResourceTemplate, because different serializers
	// might have different fields order even if the objects are identical.

	// Compare TriggerTemplates ignoring raw TriggerResourceTemplate
	triggerTemplatesDiff := cmp.Diff(existingTriggerTemplate, expectedTriggerTemplate, triggerTemplateDiffOpts...)
	if len(triggerTemplatesDiff) > 0 {
		log.Info("Trigger template is not up to date, rebuild.")
		log.Info(fmt.Sprintf("Diff:\n%s", triggerTemplatesDiff))
		return true, nil
	}

	// Deserialize and compare TriggerResourceTemplates

	var expectedTriggerResourceTemplate tektonapi.PipelineRun
	if err := json.Unmarshal(expectedTriggerTemplate.Spec.ResourceTemplates[0].Raw, &expectedTriggerResourceTemplate); err != nil {
		return false, err
	}

	var existingTriggerResourceTemplate tektonapi.PipelineRun
	if err := json.Unmarshal(existingTriggerTemplate.Spec.ResourceTemplates[0].Raw, &existingTriggerResourceTemplate); err != nil {
		return false, err
	}

	triggerResourceTemplateDiff := cmp.Diff(existingTriggerResourceTemplate, expectedTriggerResourceTemplate, triggerResourceTemplateDiffOpts...)
	if len(triggerResourceTemplateDiff) > 0 {
		log.Info("Trigger resource template is not up to date, rebuild.")
		log.Info(fmt.Sprintf("Diff:\n%s", triggerResourceTemplateDiff))
		return true, nil
	}

	log.Info("Trigger template is up to date, rebuild is not needed.")
	return false, nil
}

// SubmitNewBuild creates a new PipelineRun to build a new image for the given component.
func (r *ComponentBuildReconciler) SubmitNewBuild(ctx context.Context, component appstudiov1alpha1.Component) error {
	log := r.Log.WithValues("Namespace", component.Namespace, "Application", component.Spec.Application, "Component", component.Name)
	log.Info("New build submitted")

	// TODO delete creation of gitops build objects(except PipelineRun) when build part of gitops repository will be respected

	workspaceStorage := gitops.GenerateCommonStorage(component, "appstudio")
	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: workspaceStorage.Name, Namespace: workspaceStorage.Namespace}, pvc)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Client.Create(ctx, &workspaceStorage)
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

	gitSecretName := component.Spec.Source.GitSource.Secret
	// Make the Secret ready for consumption by Tekton.
	if gitSecretName != "" {
		gitSecret := corev1.Secret{}
		err = r.Client.Get(ctx, types.NamespacedName{Name: gitSecretName, Namespace: component.Namespace}, &gitSecret)
		if err != nil {
			log.Error(err, fmt.Sprintf("Secret %s is missing", gitSecretName))
			return err
		} else {
			if gitSecret.Annotations == nil {
				gitSecret.Annotations = map[string]string{}
			}

			gitHost, _ := getGitProvider(component.Spec.Source.GitSource.URL)

			// doesn't matter if it was present, we will always override.
			gitSecret.Annotations["tekton.dev/git-0"] = gitHost
			err = r.Client.Update(ctx, &gitSecret)
			if err != nil {
				log.Error(err, fmt.Sprintf("Secret %s  update failed", gitSecretName))
				return err
			}
		}
	}

	pipelinesServiceAccount := corev1.ServiceAccount{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: "pipeline", Namespace: component.Namespace}, &pipelinesServiceAccount)
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

	triggerTemplate, err := gitops.GenerateTriggerTemplate(component)
	if err != nil {
		log.Error(err, "Unable to generate triggerTemplate ")
		return err
	}
	err = controllerutil.SetOwnerReference(&component, triggerTemplate, r.Scheme)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to set owner reference for %v", triggerTemplate))
	}

	existingTriggerTemplate := &triggersapi.TriggerTemplate{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: triggerTemplate.Name, Namespace: triggerTemplate.Namespace}, existingTriggerTemplate)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Client.Create(ctx, triggerTemplate)
			if err != nil {
				log.Error(err, fmt.Sprintf("Unable to create triggerTemplate %v", triggerTemplate))
				return err
			}
			log.Info(fmt.Sprintf("TriggerTemplate created %v", triggerTemplate.Name))
		} else {
			log.Error(err, fmt.Sprintf("Unable to get triggerTemplate %s", triggerTemplate.Name))
			return err
		}
	} else {
		existingTriggerTemplate.Spec = triggerTemplate.Spec
		err = r.Client.Update(ctx, existingTriggerTemplate)
		if err != nil {
			log.Error(err, fmt.Sprintf("Unable to update triggerTemplate %v", existingTriggerTemplate))
			return err
		}
		log.Info(fmt.Sprintf("TriggerTemplate updated %v", triggerTemplate.Name))
	}

	eventListener := gitops.GenerateEventListener(component, *triggerTemplate)
	err = controllerutil.SetOwnerReference(&component, &eventListener, r.Scheme)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to set owner reference for %v", eventListener))
		return err
	}
	err = r.Client.Get(ctx, types.NamespacedName{Name: eventListener.Name, Namespace: eventListener.Namespace}, &triggersapi.EventListener{})
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Client.Create(ctx, &eventListener)
			if err != nil {
				log.Error(err, fmt.Sprintf("Unable to create eventListener %v", eventListener))
				return err
			}
		} else {
			log.Error(err, fmt.Sprintf("Unable to get eventListener %v", eventListener))
			return err
		}
	}
	log.Info(fmt.Sprintf("Eventlistener created/updated %v", eventListener.Name))

	initialBuild := gitops.GenerateInitialBuildPipelineRun(component)
	err = controllerutil.SetOwnerReference(&component, &initialBuild, r.Scheme)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to set owner reference for %v", initialBuild))
	}
	err = r.Client.Create(ctx, &initialBuild)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to create the build PipelineRun %v", initialBuild))
		return err
	}
	log.Info(fmt.Sprintf("Pipeline created %v", initialBuild))

	webhook := gitops.GenerateBuildWebhookRoute(component)
	err = controllerutil.SetOwnerReference(&component, &webhook, r.Scheme)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to set owner reference for %v", webhook))
	}
	err = r.Client.Get(ctx, types.NamespacedName{Name: webhook.Name, Namespace: webhook.Namespace}, &routev1.Route{})
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Client.Create(ctx, &webhook)
			if err != nil {
				log.Error(err, fmt.Sprintf("Unable to create webhook %v", webhook.Name))
				return err
			}
		} else {
			log.Error(err, fmt.Sprintf("Unable to get webhook %v", webhook.Name))
			return err
		}
	}

	return err
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
