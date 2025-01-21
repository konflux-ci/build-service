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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	applicationapi "github.com/konflux-ci/application-api/api/v1alpha1"
	releaseapi "github.com/konflux-ci/release-service/api/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"golang.org/x/exp/slices"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"knative.dev/pkg/apis"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	l "github.com/konflux-ci/build-service/pkg/logs"
)

const (
	contextTimeout = 300 * time.Second
	// PipelineRunTypeLabelName contains the type of the PipelineRunType
	PipelineRunTypeLabelName = "pipelines.appstudio.openshift.io/type"
	// PipelineRunBuildType is the type denoting a build PipelineRun.
	PipelineRunBuildType = "build"
	// PacEventTypeAnnotationName represents the current event type
	PacEventTypeAnnotationName = "pipelinesascode.tekton.dev/event-type"
	PacEventPushType           = "push"
	PacEventIncomingType       = "incoming"
	ImageUrlParamName          = "IMAGE_URL"
	ImageDigestParamName       = "IMAGE_DIGEST"

	NudgeProcessedAnnotationName = "build.appstudio.openshift.io/component-nudge-processed"
	NudgeFinalizer               = "build.appstudio.openshift.io/build-nudge-finalizer"
	FailureCountAnnotationName   = "build.appstudio.openshift.io/build-nudge-failures"
	NudgeFilesAnnotationName     = "build.appstudio.openshift.io/build-nudge-files"

	ComponentNudgedEventType      = "ComponentNudged"
	ComponentNudgeFailedEventType = "ComponentNudgeFailed"
	MaxAttempts                   = 3
	KubeApiUpdateMaxAttempts      = 5

	FailureRetryTime  = time.Minute * 5 // We retry after 5 minutes on failure
	DefaultNudgeFiles = ".*Dockerfile.*, .*.yaml, .*Containerfile.*"
)

// The amount of time we wait before attempting to update the component, to try and avoid contention issues
// This is not a constant so tests don't have to wait
var delayTime = time.Second * 10

// ComponentDependencyUpdateReconciler reconciles a PipelineRun object
type ComponentDependencyUpdateReconciler struct {
	Client                       client.Client
	ApiReader                    client.Reader
	Scheme                       *runtime.Scheme
	EventRecorder                record.EventRecorder
	ComponentDependenciesUpdater ComponentDependenciesUpdater
}

type BuildResult struct {
	BuiltImageRepository     string
	BuiltImageTag            string
	Digest                   string
	DistributionRepositories []string
	FileMatches              string
	Component                *applicationapi.Component
}

type RepositoryCredentials struct {
	SecretName string
	RepoName   string
	UserName   string
	Password   string
}

type RepositoryConfigAuth struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Email    string `json:"email,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

// SetupController creates a new Integration reconciler and adds it to the Manager.
func (r *ComponentDependencyUpdateReconciler) SetupWithManager(manager ctrl.Manager) error {
	return setupControllerWithManager(manager, r)
}

// setupControllerWithManager sets up the controller with the Manager which monitors new PipelineRuns and filters
// out pipelines we don't need
func setupControllerWithManager(manager ctrl.Manager, reconciler *ComponentDependencyUpdateReconciler) error {
	return ctrl.NewControllerManagedBy(manager).
		For(&tektonapi.PipelineRun{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				new, ok := e.Object.(*tektonapi.PipelineRun)
				if !ok {
					return false
				}
				return IsBuildPushPipelineRun(new)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				new, ok := e.ObjectNew.(*tektonapi.PipelineRun)
				if !ok {
					return false
				}
				if !IsBuildPushPipelineRun(new) {
					return false
				}

				// Ensure we have not finished processing
				if new.ObjectMeta.Annotations != nil && new.ObjectMeta.Annotations[NudgeProcessedAnnotationName] != "" {
					return false
				}
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return true
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return true
			},
		})).
		Complete(reconciler)
}

// The following line for configmaps is informational, the actual permissions are defined in component_build_controller.
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=create;get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=appstudio.redhat.com,resources=components/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=get;list;watch;create;update;patch;delete;deletecollection
// +kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
func (r *ComponentDependencyUpdateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	log := ctrllog.FromContext(ctx).WithName("ComponentNudge")
	ctx = ctrllog.IntoContext(ctx, log)

	pipelineRun := &tektonapi.PipelineRun{}
	err := r.Client.Get(ctx, req.NamespacedName, pipelineRun)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get pipelineRun")
		return ctrl.Result{}, err
	}
	if pipelineRun.CreationTimestamp.Add(delayTime).After(time.Now()) {
		// These objects are super contested at creation, we just wait 10s before attempting anything
		return ctrl.Result{RequeueAfter: delayTime}, nil
	}

	component, err := GetComponentFromPipelineRun(r.Client, ctx, pipelineRun)
	if err != nil || component == nil {
		log.Error(err, "failed to get component")
		// In case the component was deleted while running the pipeline
		if controllerutil.ContainsFinalizer(pipelineRun, NudgeFinalizer) {
			patch := client.MergeFrom(pipelineRun.DeepCopy())
			return r.removePipelineFinalizer(ctx, pipelineRun, patch)
		}

		// When component doesn't exist handle it as permanent error
		if errors.IsNotFound(err) {
			log.Error(err, "component doesn't exist")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if len(component.Spec.BuildNudgesRef) == 0 {
		// In case the nudge was removed while running the pipeline
		if controllerutil.ContainsFinalizer(pipelineRun, NudgeFinalizer) {
			patch := client.MergeFrom(pipelineRun.DeepCopy())
			return r.removePipelineFinalizer(ctx, pipelineRun, patch)
		}
		return ctrl.Result{}, nil
	}
	log.Info("component has BuildNudgesRef set", "ComponentName", component.Name, "BuildNudgesRef", component.Spec.BuildNudgesRef)

	// verify that there exist some components to be nudged
	allComponents := applicationapi.ComponentList{}
	err = r.Client.List(ctx, &allComponents, client.InNamespace(pipelineRun.Namespace))
	if err != nil {
		log.Error(err, "failed to list components in namespace")
		return ctrl.Result{}, err
	}

	nudgedComponentsCount := 0
	for i := range allComponents.Items {
		comp := allComponents.Items[i]
		if slices.Contains(component.Spec.BuildNudgesRef, comp.Name) {
			nudgedComponentsCount++
			log.Info("component in BuildNudgesRef exist", "ComponentName", comp.Name)
		}
	}
	if nudgedComponentsCount == 0 {
		log.Info("no components in BuildNudgesRef exist", "BuildNudgesRef", component.Spec.BuildNudgesRef)
		return ctrl.Result{}, nil
	}

	if pipelineRun.IsDone() || pipelineRun.Status.CompletionTime != nil || pipelineRun.DeletionTimestamp != nil {
		result, err := r.verifyUpToDate(ctx, pipelineRun)
		if err != nil {
			return reconcile.Result{}, err
		} else if result != nil {
			return *result, nil
		}
		log.Info("PipelineRun complete")
		if controllerutil.ContainsFinalizer(pipelineRun, NudgeFinalizer) || pipelineRun.Annotations == nil || pipelineRun.Annotations[NudgeProcessedAnnotationName] == "" {
			log.Info("will run renovate job")
			// Pipeline run is done and we have not cleared the finalizer yet
			// We need to perform our nudge
			patch := client.MergeFrom(pipelineRun.DeepCopy())
			return r.handleCompletedBuild(ctx, pipelineRun, component, patch)
		}
	} else if !controllerutil.ContainsFinalizer(pipelineRun, NudgeFinalizer) {
		result, err := r.verifyUpToDate(ctx, pipelineRun)
		if err != nil || result != nil {
			return *result, err
		}
		log.Info("adding finalizer for component nudge")
		// We add a finalizer to make sure we see the run before it is deleted
		// As tekton results should aggressivly delete when pruning is enabled
		patch := client.MergeFrom(pipelineRun.DeepCopy())
		controllerutil.AddFinalizer(pipelineRun, NudgeFinalizer)
		err = r.Client.Patch(ctx, pipelineRun, patch)
		if err == nil {
			return reconcile.Result{}, nil
		} else if errors.IsConflict(err) {
			log.Error(err, "failed to add finalizer due to conflict, retrying in one second")
			return reconcile.Result{RequeueAfter: time.Second}, nil
		} else {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *ComponentDependencyUpdateReconciler) verifyUpToDate(ctx context.Context, pipelineRun *tektonapi.PipelineRun) (*ctrl.Result, error) {
	// These objects are so heavily contented that we always grab the latest copy from the
	// API server and verify we are up-to-date
	currentPipelineRun := &tektonapi.PipelineRun{}
	err := r.ApiReader.Get(ctx, types.NamespacedName{Namespace: pipelineRun.Namespace, Name: pipelineRun.Name}, currentPipelineRun)
	if err != nil {
		return nil, err
	}
	if currentPipelineRun.ResourceVersion != pipelineRun.ResourceVersion {
		ctrllog.FromContext(ctx).Info("returning early as resource is out of date")
		return &ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return nil, nil
}

// handleCompletedBuild will perform a 'nudge' updating dependent downstream components.
// This will involve creating a PR updating their references to our images to the newly produced image
func (r *ComponentDependencyUpdateReconciler) handleCompletedBuild(ctx context.Context, pipelineRun *tektonapi.PipelineRun, updatedComponent *applicationapi.Component, patch client.Patch) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	success := pipelineRun.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
	if !success {
		log.Info("not performing nudge as pipeline failed")
		return r.removePipelineFinalizer(ctx, pipelineRun, patch)
	}
	// find the image and digest we want to update to
	image := ""
	digest := ""
	for _, r := range pipelineRun.Status.Results {
		if r.Name == ImageDigestParamName {
			digest = r.Value.StringVal
		} else if r.Name == ImageUrlParamName {
			image = r.Value.StringVal
		}
	}
	// Failure to find is a permanent error, so remove the finalizer
	if image == "" {
		log.Error(fmt.Errorf("unable to find %s param on PipelineRun, not performing nudge", ImageUrlParamName), "no image url result")
		return r.removePipelineFinalizer(ctx, pipelineRun, patch)
	}
	if digest == "" {
		log.Error(fmt.Errorf("unable to find %s param on PipelineRun, not performing nudge", ImageDigestParamName), "no image digest result")
		return r.removePipelineFinalizer(ctx, pipelineRun, patch)
	}
	log.Info("image used for nudging", "ImageName", image, "Digest", digest)

	tag := ""
	repo := image
	index := strings.LastIndex(image, ":")
	if index != -1 {
		repo = image[0:index]
		tag = image[index+1:]
	}
	// find any configurations for files to nudge in
	if pipelineRun.Annotations == nil {
		pipelineRun.Annotations = map[string]string{}
	}
	nudgeFiles := pipelineRun.Annotations[NudgeFilesAnnotationName]
	if nudgeFiles == "" {
		nudgeFiles = DefaultNudgeFiles
	} else {
		log.Info("custom nudging files specified in the annotation", "AnnotationName", NudgeFilesAnnotationName, "NudgeFiles", nudgeFiles)
	}

	components := applicationapi.ComponentList{}
	err := r.Client.List(ctx, &components, client.InNamespace(pipelineRun.Namespace))
	if err != nil {
		log.Error(err, "failed to list components in namespace")
		return ctrl.Result{}, err
	}
	// Now look for distribution repositories
	// We do this by looking through ReleasePlanAdmission objects

	retryTime := FailureRetryTime
	immediateRetry := false

	componentsToUpdate := []applicationapi.Component{}

	distibutionRepositories := []string{}
	releasePlanAdmissions := releaseapi.ReleasePlanAdmissionList{}
	err = r.Client.List(ctx, &releasePlanAdmissions)
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("searching for releasePlanAdmissions for component to find distribution repos", "ComponentName", updatedComponent.Name)
	for _, admission := range releasePlanAdmissions.Items {
		if admission.Spec.Origin == pipelineRun.Namespace && admission.Spec.Data != nil {
			log.Info("considering ReleaseAdmissionPlan", "plan", admission.Name, "origin", admission.Spec.Origin, "namespace", admission.Namespace)
			data := struct {
				Mapping struct {
					Components []struct {
						Name       string
						Repository string
					}
				}
			}{}
			err := json.Unmarshal(admission.Spec.Data.Raw, &data)
			if err != nil {
				log.Error(err, fmt.Sprintf("unable to parse ReleasePlanAdmission %s/%s", admission.Namespace, admission.Name))
			}
			for _, compMapping := range data.Mapping.Components {
				if compMapping.Name == updatedComponent.Name {
					log.Info("added distribution repo for component", "repo", compMapping.Repository)
					distibutionRepositories = append(distibutionRepositories, compMapping.Repository)
					registryRedhatMapping := mapToRegistryRedhatIo(compMapping.Repository)
					if registryRedhatMapping != "" {
						distibutionRepositories = append(distibutionRepositories, registryRedhatMapping)
					}
				}
			}
		}
	}

	for i := range components.Items {
		comp := components.Items[i]
		if slices.Contains(updatedComponent.Spec.BuildNudgesRef, comp.Name) {
			componentsToUpdate = append(componentsToUpdate, comp)
		}
	}

	updatedOutputImage := updatedComponent.Spec.ContainerImage
	imageRepositoryHost := strings.Split(updatedOutputImage, "/")[0]

	imageRepositoryUsername, imageRepositoryPassword, err := r.getImageRepositoryCredentials(ctx, pipelineRun.Namespace, updatedOutputImage)
	if err != nil {
		// when we can't find credential for repository, remove pipeline finalizer and return error
		_, errRemoveFinalizer := r.removePipelineFinalizer(ctx, pipelineRun, patch)
		if errRemoveFinalizer != nil {
			return ctrl.Result{}, errRemoveFinalizer
		}

		return ctrl.Result{}, err
	}

	var nudgeErr error
	var targets []updateTarget

	newTargets := r.ComponentDependenciesUpdater.GetUpdateTargetsGithubApp(ctx, componentsToUpdate, imageRepositoryHost, imageRepositoryUsername, imageRepositoryPassword)
	log.Info("found new targets for GitHub app", "targets", len(newTargets))
	if len(newTargets) > 0 {
		targets = append(targets, newTargets...)
	}

	newTargets = r.ComponentDependenciesUpdater.GetUpdateTargetsBasicAuth(ctx, componentsToUpdate, imageRepositoryHost, imageRepositoryUsername, imageRepositoryPassword)
	log.Info("found new targets for basic auth", "targets", len(newTargets))
	if len(newTargets) > 0 {
		targets = append(targets, newTargets...)
	}

	gitRepoAtShaLink := pipelineRun.Annotations[gitRepoAtShaAnnotationName]
	if len(targets) > 0 {
		log.Info("will create renovate job")
		buildResult := BuildResult{BuiltImageRepository: repo, BuiltImageTag: tag, Digest: digest, Component: updatedComponent, DistributionRepositories: distibutionRepositories, FileMatches: nudgeFiles}
		nudgeErr = r.ComponentDependenciesUpdater.CreateRenovaterPipeline(ctx, pipelineRun.Namespace, targets, true, &buildResult, gitRepoAtShaLink)
	} else {
		log.Info("no targets found to update")
	}

	if nudgeErr != nil {
		componentDesc := ""

		for _, comp := range componentsToUpdate {
			if componentDesc != "" {
				componentDesc += ", "
			}
			componentDesc += comp.Namespace + "/" + comp.Name
		}
		log.Error(nudgeErr, fmt.Sprintf("component update of components %s as a result of a build of %s failed", componentDesc, updatedComponent.Name), l.Audit, "true")

		if pipelineRun.Annotations == nil {
			pipelineRun.Annotations = map[string]string{}
		}
		existing := pipelineRun.Annotations[FailureCountAnnotationName]
		if existing == "" {
			existing = "0"
		}
		failureCount, err := strconv.Atoi(existing)
		if err != nil {
			log.Error(err, "failed to parse retry count, not retrying")
			return r.removePipelineFinalizer(ctx, pipelineRun, patch)
		}
		failureCount = failureCount + 1

		pipelineRun.Annotations[FailureCountAnnotationName] = strconv.Itoa(failureCount)

		r.EventRecorder.Event(updatedComponent, corev1.EventTypeWarning, ComponentNudgeFailedEventType, fmt.Sprintf("component update failed as a result of a build for %s, retry %d/%d", updatedComponent.Name, failureCount, MaxAttempts))

		if failureCount >= MaxAttempts {
			// We are at the failure limit, nothing much we can do
			log.Info("not retrying as max failure limit has been reached", l.Audit, "true")
			return r.removePipelineFinalizer(ctx, pipelineRun, patch)
		}
		log.Info(fmt.Sprintf("failed to update component dependencies, retry %d/%d", failureCount, MaxAttempts))
		err = r.Client.Patch(ctx, pipelineRun, patch)
		if err != nil {
			// If we fail to update just return and let requeue handle it
			// We can't really do anything else
			// This does mean components may get nudged a second time, but it is idempotent anyway
			return ctrl.Result{}, err
		}
		if immediateRetry {
			return reconcile.Result{RequeueAfter: time.Millisecond}, nil
		} else {
			return reconcile.Result{RequeueAfter: retryTime * time.Duration(failureCount)}, nil
		}
	}

	_, err = r.removePipelineFinalizer(ctx, pipelineRun, patch)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Now we need to look for 'stale' pipelines.
	// These are defined as pipelines that are younger than this one, that target the same component.
	// If there are two pushes very close together variances in pipeline run times may mean that the
	// older one finishes last. In this case we want to mark the older one as already nudged and remove
	// the finalizer.

	pipelines := tektonapi.PipelineRunList{}
	err = r.Client.List(ctx, &pipelines, client.InNamespace(pipelineRun.Namespace), client.MatchingLabels{PipelineRunTypeLabelName: PipelineRunBuildType, ComponentNameLabelName: updatedComponent.Name})
	if err != nil {
		// I don't think we want to retry this, it should be really rare anyway
		// and would require an even more complex label based state machine.
		log.Error(err, "failed to check for stale pipeline runs, this operation will not be retried", l.Audit, "true")
		return ctrl.Result{}, nil
	}
	var finalizerError error
	for i := range pipelines.Items {
		possiblyStalePr := pipelines.Items[i]
		if possiblyStalePr.Annotations == nil || (!strings.EqualFold(possiblyStalePr.Annotations[PacEventTypeAnnotationName], PacEventPushType) && !strings.EqualFold(possiblyStalePr.Annotations[PacEventTypeAnnotationName], PacEventIncomingType)) || possiblyStalePr.Name == pipelineRun.Name {
			continue
		}
		if possiblyStalePr.Status.CompletionTime == nil && possiblyStalePr.CreationTimestamp.Before(&pipelineRun.CreationTimestamp) {
			log.Info(fmt.Sprintf("marking PipelineRun %s as nudged, as it is stale", possiblyStalePr.Name))
			if possiblyStalePr.Annotations == nil {
				possiblyStalePr.Annotations = map[string]string{}
			}
			_, err := r.removePipelineFinalizer(ctx, &possiblyStalePr, patch)
			if err != nil {
				finalizerError = err
				log.Error(err, "failed to update stale pipeline run", l.Audit, "true")
			}
		}

	}
	return ctrl.Result{}, finalizerError
}

// removePipelineFinalizer will remove the finalizer, and add an annotation to indicate we are done with this pipeline run
// We can't use just the presence or absence of the finalizer, as there is some situations where we might not have seen the
// run until it is completed, e.g. if the controller was down.
func (r *ComponentDependencyUpdateReconciler) removePipelineFinalizer(ctx context.Context, pipelineRun *tektonapi.PipelineRun, patch client.Patch) (ctrl.Result, error) {

	if pipelineRun.Annotations == nil {
		pipelineRun.Annotations = map[string]string{}
	}
	pipelineRun.Annotations[NudgeProcessedAnnotationName] = "true"
	controllerutil.RemoveFinalizer(pipelineRun, NudgeFinalizer)
	err := r.Client.Patch(ctx, pipelineRun, patch)
	if err != nil {
		if !errors.IsConflict(err) {
			log := ctrllog.FromContext(ctx)
			// We don't log/fire events on conflicts, they are part of normal operation,
			// especially as these are highly contended objects
			log.Error(err, "unable to remove pipeline run finalizer")
			r.EventRecorder.Event(pipelineRun, corev1.EventTypeWarning, ComponentNudgedEventType, fmt.Sprintf("failed to remove finalizer from %s", pipelineRun.Name))
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// getImageRepositoryCredentials returns username and password for image repository
// it is searching all dockerconfigjson type secrets which are linked to the service account
func (r *ComponentDependencyUpdateReconciler) getImageRepositoryCredentials(ctx context.Context, namespace, updatedOutputImage string) (string, string, error) {
	log := ctrllog.FromContext(ctx)

	// get service account and gather linked secrets
	pipelinesServiceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: namespace}, pipelinesServiceAccount)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to read service account %s in namespace %s", buildPipelineServiceAccountName, namespace), l.Action, l.ActionView)
		return "", "", err
	}

	linkedSecretNames := []string{}
	for _, secret := range pipelinesServiceAccount.Secrets {
		linkedSecretNames = append(linkedSecretNames, secret.Name)
	}

	if len(linkedSecretNames) == 0 {
		err = fmt.Errorf("No secrets linked to service account %s in namespace %s", buildPipelineServiceAccountName, namespace)
		log.Error(err, "no linked secrets")
		return "", "", err
	}
	log.Info("secrets linked to service account", "count", len(linkedSecretNames))

	// get all docker config json secrets
	allImageRepoSecrets := &corev1.SecretList{}
	opts := client.ListOption(&client.MatchingFields{"type": string(corev1.SecretTypeDockerConfigJson)})

	if err := r.Client.List(ctx, allImageRepoSecrets, client.InNamespace(namespace), opts); err != nil {
		return "", "", fmt.Errorf("failed to list secrets of type %s in %s namespace: %w", corev1.SecretTypeDockerConfigJson, namespace, err)
	}
	log.Info("found docker config secrets secrets", "count", len(allImageRepoSecrets.Items))

	type DockerConfigJson struct {
		ConfigAuths map[string]RepositoryConfigAuth `json:"auths"`
	}

	filteredSecretsData := []RepositoryCredentials{}
	for _, secret := range allImageRepoSecrets.Items {
		isSecretLinked := false

		for _, linkedSecret := range linkedSecretNames {
			if secret.Name == linkedSecret {
				isSecretLinked = true
				break
			}
		}
		if !isSecretLinked {
			continue
		}

		dockerConfigObject := &DockerConfigJson{}
		if err = json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], dockerConfigObject); err != nil {
			log.Error(err, fmt.Sprintf("unable to parse docker json config in the secret %s", secret.Name))
			continue
		}

		for repoName, repoAuth := range dockerConfigObject.ConfigAuths {
			if repoAuth.Username != "" && repoAuth.Password != "" {
				filteredSecretsData = append(filteredSecretsData, RepositoryCredentials{SecretName: secret.Name, RepoName: repoName, UserName: repoAuth.Username, Password: repoAuth.Password})
			} else {
				if repoAuth.Auth == "" {
					log.Error(fmt.Errorf("password and username and auth are empty in auth config for repository %s", repoName), "no valid auth")
					continue
				} else {
					decodedAuth, err := base64.StdEncoding.DecodeString(repoAuth.Auth)
					if err != nil {
						log.Error(err, fmt.Sprintf("unable to decode docker config json auth for repository %s in the secret %s", repoName, secret.Name))
						continue
					}
					authParts := strings.Split(string(decodedAuth), ":")
					filteredSecretsData = append(filteredSecretsData, RepositoryCredentials{SecretName: secret.Name, RepoName: repoName, UserName: authParts[0], Password: authParts[1]})
				}
			}
		}
	}

	imageRepositoryUsername, imageRepositoryPassword, err := GetMatchedCredentialForImageRepository(ctx, updatedOutputImage, filteredSecretsData)
	if err != nil {
		log.Error(err, fmt.Sprintf("unable to find credential for repository %s", updatedOutputImage))
		return "", "", err
	}
	return imageRepositoryUsername, imageRepositoryPassword, nil
}

// GetMatchedCredentialForImageRepository returns credentials for image repository
// it is trying to search for credential for the given image repository from all provided credentials
// first it tries to find exact repo match
// then it tries to find the best (the longest) partial match
func GetMatchedCredentialForImageRepository(ctx context.Context, outputImage string, imageRepoSecrets []RepositoryCredentials) (string, string, error) {
	log := ctrllog.FromContext(ctx)

	repoPath := outputImage
	if strings.Contains(outputImage, "@") {
		repoPath = strings.Split(outputImage, "@")[0]
	} else {
		if strings.Contains(outputImage, ":") {
			repoPath = strings.Split(outputImage, ":")[0]
		}
	}
	repoPath = strings.TrimSuffix(repoPath, "/")

	username := ""
	password := ""
	// first check for credential which matches full repository path
	for _, credential := range imageRepoSecrets {
		credentialRepo := strings.TrimSuffix(credential.RepoName, "/")
		if repoPath == credentialRepo {
			log.Info("found full match of repository in auth", "repo", repoPath, "secretName", credential.SecretName)
			username = credential.UserName
			password = credential.Password
			break
		}
	}
	if username != "" && password != "" {
		return username, password, nil

	}

	// check for partial match, get the most complete match
	// if there is multiple secrets for registry and some partial match, upload sbom would fail anyway, because it chooses them randomly (until cosign fixes it)
	repoParts := strings.Split(repoPath, "/")
	for {
		repoParts = repoParts[:len(repoParts)-1]
		if len(repoParts) == 0 {
			break
		}
		partialRepo := strings.Join(repoParts, "/")

		for _, credential := range imageRepoSecrets {
			credentialRepo := strings.TrimSuffix(credential.RepoName, "/")
			if partialRepo == credentialRepo {
				log.Info("partial match found of repository in auth", "repo", partialRepo, "secretName", credential.SecretName)
				if credential.UserName != "" && credential.Password != "" {
					username = credential.UserName
					password = credential.Password
					return username, password, nil
				}
				log.Info("credential in the auth for repository is missing password or username", "repo", partialRepo, "secretName", credential.SecretName)
			}
		}
	}

	log.Info("no credentials found for repo", "repo", repoPath)
	return "", "", fmt.Errorf("No credentials found for repository %s ", repoPath)
}

func IsBuildPushPipelineRun(object client.Object) bool {
	if pipelineRun, ok := object.(*tektonapi.PipelineRun); ok {

		// Ensure the PipelineRun belongs to a Component
		if pipelineRun.Labels == nil || pipelineRun.Labels[ComponentNameLabelName] == "" {
			// PipelineRun does not belong to a Component
			return false
		}
		if pipelineRun.Labels != nil && pipelineRun.Annotations != nil {
			if pipelineRun.Labels[PipelineRunTypeLabelName] == PipelineRunBuildType && (strings.EqualFold(pipelineRun.Annotations[PacEventTypeAnnotationName], PacEventPushType) || strings.EqualFold(pipelineRun.Annotations[PacEventTypeAnnotationName], PacEventIncomingType)) {
				return true
			}
		}
	}
	return false
}

// GetComponentFromPipelineRun loads from the cluster the Component referenced in the given PipelineRun. If the PipelineRun doesn't
// specify a Component we return nil, if the component is not specified we return an error
func GetComponentFromPipelineRun(c client.Client, ctx context.Context, pipelineRun *tektonapi.PipelineRun) (*applicationapi.Component, error) {
	if componentName, found := pipelineRun.Labels[ComponentNameLabelName]; found {
		component := &applicationapi.Component{}
		err := c.Get(ctx, types.NamespacedName{
			Namespace: pipelineRun.Namespace,
			Name:      componentName,
		}, component)

		if err != nil {
			return nil, err
		}

		return component, nil
	}

	return nil, nil
}

// See https://issues.redhat.com/browse/KFLUXBUGS-1233
// This will map repsitories of the form 'quay.io/redhat-prod/foo----bar' to 'registry.redhat.io/foo/bar'
func mapToRegistryRedhatIo(repo string) string {
	prodRegex, err := regexp.Compile(`^quay.io/redhat-prod/(.*)----(.*)$`)
	if err != nil {
		return ""
	}

	results := prodRegex.FindStringSubmatch(repo)
	if results != nil {
		return "registry.redhat.io/" + results[1] + "/" + results[2]
	}

	// try handling the stage registry
	stageRegex, err := regexp.Compile(`^quay.io/redhat-pending/(.*)----(.*)$`)
	if err != nil {
		return ""
	}
	results = stageRegex.FindStringSubmatch(repo)
	if results != nil {
		return "registry.stage.redhat.io/" + results[1] + "/" + results[2]
	}

	return ""
}
