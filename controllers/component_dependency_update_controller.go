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
	"encoding/json"
	"fmt"
	applicationapi "github.com/redhat-appstudio/application-api/api/v1alpha1"
	l "github.com/redhat-appstudio/build-service/pkg/logs"
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
	"strconv"
	"strings"
	"text/template"
	"time"
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
	ImageUrlParamName          = "IMAGE_URL"
	ImageDigestParamName       = "IMAGE_DIGEST"

	NudgeProcessedAnnotationName = "build.appstudio.openshift.io/component-nudge-processed"
	NudgeFinalizer               = "build.appstudio.openshift.io/build-nudge-finalizer"
	FailureCountAnnotationName   = "build.appstudio.openshift.io/build-nudge-failures"

	ComponentNudgedEventType      = "ComponentNudged"
	ComponentNudgeFailedEventType = "ComponentNudgeFailed"
	MaxAttempts                   = 3
	KubeApiUpdateMaxAttempts      = 5

	FailureRetryTime = time.Minute * 5 // We retry after 5 minutes on failure
)

// ComponentDependencyUpdateReconciler reconciles a PipelineRun object
type ComponentDependencyUpdateReconciler struct {
	client.Client
	ApiReader      client.Reader
	Scheme         *runtime.Scheme
	EventRecorder  record.EventRecorder
	UpdateFunction UpdateComponentDependenciesFunction
}

type BuildResult struct {
	BuiltImageRepository     string
	BuiltImageTag            string
	Digest                   string
	DistributionRepositories []string
	Component                *applicationapi.Component
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

type UpdateComponentDependenciesFunction = func(ctx context.Context, client client.Client, scheme *runtime.Scheme, eventRecorder record.EventRecorder, downstreamComponents []applicationapi.Component, result *BuildResult) (immediateRetry bool, err error)

var DefaultUpdateFunction = DefaultDependenciesUpdate

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
	err := r.Get(ctx, req.NamespacedName, pipelineRun)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get pipelineRun")
		return ctrl.Result{}, err
	}

	component, err := GetComponentFromPipelineRun(r.Client, ctx, pipelineRun)
	if err != nil || component == nil {
		log.Error(err, "failed to get component")
		return ctrl.Result{}, err
	}

	if len(component.Spec.BuildNudgesRef) == 0 {
		log.Info(fmt.Sprintf("component %s has no BuildNudgesRef set", component.Name))
		return ctrl.Result{}, nil
	}
	log.Info("reconciling PipelineRun")

	if pipelineRun.IsDone() || pipelineRun.Status.CompletionTime != nil || pipelineRun.DeletionTimestamp != nil {
		log.Info("PipelineRun complete")
		// These objects are so heavily contented that we always grab the latest copy from the
		// API server to reduce the chance of conflicts
		err = r.ApiReader.Get(ctx, req.NamespacedName, pipelineRun)
		if err != nil {
			return ctrl.Result{}, err
		}
		if controllerutil.ContainsFinalizer(pipelineRun, NudgeFinalizer) || pipelineRun.Annotations == nil || pipelineRun.Annotations[NudgeProcessedAnnotationName] == "" {
			log.Info("running renovate job")
			// Pipeline run is done and we have not cleared the finalizer yet
			// We need to perform our nudge
			return r.handleCompletedBuild(ctx, pipelineRun, component)
		}
	} else if !controllerutil.ContainsFinalizer(pipelineRun, NudgeFinalizer) {
		log.Info("adding finalizer for component nudge")
		// These objects are so heavily contented that we always grab the latest copy from the
		// API server to reduce the chance of conflicts
		err := r.ApiReader.Get(ctx, req.NamespacedName, pipelineRun)
		if err != nil {
			return ctrl.Result{}, err
		}
		// We add a finalizer to make sure we see the run before it is deleted
		// As tekton results should aggressivly delete when pruning is enabled
		controllerutil.AddFinalizer(pipelineRun, NudgeFinalizer)
		err = r.Client.Update(ctx, pipelineRun)
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

// handleCompletedBuild will perform a 'nudge' updating dependent downstream components.
// This will involve creating a PR updating their references to our images to the newly produced image
func (r *ComponentDependencyUpdateReconciler) handleCompletedBuild(ctx context.Context, pipelineRun *tektonapi.PipelineRun, updatedComponent *applicationapi.Component) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	success := pipelineRun.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
	if !success {
		log.Info("not performing nudge as pipeline failed")
		return r.removePipelineFinalizer(ctx, pipelineRun)
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
		return r.removePipelineFinalizer(ctx, pipelineRun)
	}
	if digest == "" {
		log.Error(fmt.Errorf("unable to find %s param on PipelineRun, not performing nudge", ImageDigestParamName), "no image digest result")
		return r.removePipelineFinalizer(ctx, pipelineRun)
	}
	tag := ""
	repo := image
	index := strings.LastIndex(image, ":")
	if index != -1 {
		repo = image[0:index]
		tag = image[index+1:]
	}

	components := applicationapi.ComponentList{}
	err := r.Client.List(ctx, &components, client.InNamespace(pipelineRun.Namespace))
	if err != nil {
		log.Error(err, "failed to list components in namespace")
		return ctrl.Result{}, err
	}

	retryTime := FailureRetryTime
	immediateRetry := false

	toUpdate := []applicationapi.Component{}

	componentDesc := ""
	for i := range components.Items {
		comp := components.Items[i]
		if comp.Spec.Application == updatedComponent.Spec.Application && slices.Contains(updatedComponent.Spec.BuildNudgesRef, comp.Name) {
			toUpdate = append(toUpdate, comp)
		}
		if componentDesc != "" {
			componentDesc += ", "
		}
		componentDesc += comp.Namespace + "/" + comp.Name
	}
	var nudgeErr error
	immediateRetry, nudgeErr = r.UpdateFunction(ctx, r.Client, r.Scheme, r.EventRecorder, toUpdate, &BuildResult{BuiltImageRepository: repo, BuiltImageTag: tag, Digest: digest, Component: updatedComponent})

	if nudgeErr != nil {

		// These objects are so heavily contented that we always grab the latest copy from the
		// API server to reduce the chance of conflicts
		err = r.ApiReader.Get(ctx, types.NamespacedName{Namespace: pipelineRun.Namespace, Name: pipelineRun.Name}, pipelineRun)
		if err != nil {
			return ctrl.Result{}, err
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
			return r.removePipelineFinalizer(ctx, pipelineRun)
		}
		failureCount = failureCount + 1

		pipelineRun.Annotations[FailureCountAnnotationName] = strconv.Itoa(failureCount)

		r.EventRecorder.Event(updatedComponent, corev1.EventTypeWarning, ComponentNudgeFailedEventType, fmt.Sprintf("component update failed as a result of a build for %s, retry %d/%d", updatedComponent.Name, failureCount, MaxAttempts))

		if failureCount >= MaxAttempts {
			// We are at the failure limit, nothing much we can do
			log.Info("not retrying as max failure limit has been reached", l.Audit, "true")
			return r.removePipelineFinalizer(ctx, pipelineRun)
		}
		log.Info(fmt.Sprintf("failed to update component dependencies, retry %d/%d", failureCount, MaxAttempts))
		err = r.Client.Update(ctx, pipelineRun)
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

	_, err = r.removePipelineFinalizer(ctx, pipelineRun)
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
		if possiblyStalePr.Annotations == nil || possiblyStalePr.Annotations[PacEventTypeAnnotationName] != PacEventPushType || possiblyStalePr.Name == pipelineRun.Name {
			continue
		}
		if possiblyStalePr.Status.CompletionTime == nil && possiblyStalePr.CreationTimestamp.Before(&pipelineRun.CreationTimestamp) {
			log.Info(fmt.Sprintf("marking PipelineRun %s as nudged, as it is stale", possiblyStalePr.Name))
			if possiblyStalePr.Annotations == nil {
				possiblyStalePr.Annotations = map[string]string{}
			}
			_, err := r.removePipelineFinalizer(ctx, &possiblyStalePr)
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
func (r *ComponentDependencyUpdateReconciler) removePipelineFinalizer(ctx context.Context, pipelineRun *tektonapi.PipelineRun) (ctrl.Result, error) {

	if pipelineRun.Annotations == nil {
		pipelineRun.Annotations = map[string]string{}
	}
	pipelineRun.Annotations[NudgeProcessedAnnotationName] = "true"
	controllerutil.RemoveFinalizer(pipelineRun, NudgeFinalizer)
	err := r.Client.Update(ctx, pipelineRun)
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

func IsBuildPushPipelineRun(object client.Object) bool {
	if pipelineRun, ok := object.(*tektonapi.PipelineRun); ok {

		// Ensure the PipelineRun belongs to a Component
		if pipelineRun.Labels == nil || pipelineRun.Labels[ComponentNameLabelName] == "" {
			// PipelineRun does not belong to a Component
			return false
		}
		if pipelineRun.Labels != nil && pipelineRun.Annotations != nil {
			if pipelineRun.Labels[PipelineRunTypeLabelName] == PipelineRunBuildType && pipelineRun.Annotations[PacEventTypeAnnotationName] == PacEventPushType {
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
func DefaultDependenciesUpdate(ctx context.Context, client client.Client, scheme *runtime.Scheme, eventRecorder record.EventRecorder, downstreamComponents []applicationapi.Component, result *BuildResult) (immediateRetry bool, err error) {
	log := ctrllog.FromContext(ctx)
	log.Info(fmt.Sprintf("reading github installations for %d components", len(downstreamComponents)))
	slug, installationsToUpdate, err := GetGithubInstallationsForComponents(ctx, client, eventRecorder, downstreamComponents)
	if err != nil || slug == "" {
		return false, err
	}
	log.Info("creating renovate job")
	err = CreateRenovaterPipeline(ctx, client, scheme, result.Component.Namespace, installationsToUpdate, slug, true, generateRenovateConfigForNudge, result)

	return false, err
}

func generateRenovateConfigForNudge(slug string, repositories []renovateRepository, context interface{}) (string, error) {
	buildResult := context.(*BuildResult)

	repositoriesData, _ := json.Marshal(repositories)
	body := `
	{{with $root := .}}
	module.exports = {
		platform: "github",
		username: "{{.Slug}}[bot]",
		gitAuthor:"{{.Slug}} <123456+{{.Slug}}[bot]@users.noreply.github.com>",
		onboarding: false,
		requireConfig: "ignored",
		repositories: {{.Repositories}},
    	enabledManagers: "regex",
		customManagers: [
			{
            	"fileMatch": [".*Dockerfile.*",".*.yaml",".*Containerfile.*"],
				"customType": "regex",
				"datasourceTemplate": "docker",
				"matchStrings": [
					"{{.BuiltImageRepository}}(:.*)?@(?<currentDigest>sha256:[a-f0-9]+)"
					{{range .DistributionRepositories}},"{{.}}(:.*)?@(?<currentDigest>sha256:[a-f0-9]+)"{{end}}
				],
				"currentValueTemplate": "{{.BuiltImageTag}}",
				"depNameTemplate": "{{.BuiltImageRepository}}",
			}
		],
		packageRules: [
		  {
			matchPackagePatterns: ["*"],
			enabled: false
		  },
		  {
		  	"matchPackageNames": ["{{.BuiltImageRepository}}", {{range .DistributionRepositories}},"{{.}}"{{end}}],
			groupName: "Component Update {{.ComponentName}}",
			branchName: "rhtap/component-updates/{{.ComponentName}}",
			commitMessageTopic: "{{.ComponentName}}",
			prFooter: "To execute skipped test pipelines write comment ` + "`/ok-to-test`" + `",
			recreateClosed: true,
			recreateWhen: "always",
			rebaseWhen: "behind-base-branch",
			enabled: true,
            followTag: "{{.BuiltImageTag}}"
		  }
		],
		forkProcessing: "enabled",
		dependencyDashboard: false
	}
	{{end}}
	`
	data := struct {
		Slug                     string
		ComponentName            string
		Repositories             string
		BuiltImageRepository     string
		BuiltImageTag            string
		Digest                   string
		DistributionRepositories []string
	}{

		Slug:                     slug,
		ComponentName:            buildResult.Component.Name,
		Repositories:             string(repositoriesData),
		BuiltImageRepository:     buildResult.BuiltImageRepository,
		BuiltImageTag:            buildResult.BuiltImageTag,
		Digest:                   buildResult.Digest,
		DistributionRepositories: buildResult.DistributionRepositories,
	}

	tmpl, err := template.New("renovate").Parse(body)
	if err != nil {
		return "", err
	}
	build := strings.Builder{}
	err = tmpl.Execute(&build, data)
	if err != nil {
		return "", err
	}
	return build.String(), nil
}
