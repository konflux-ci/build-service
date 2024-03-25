/*
Copyright 2023.

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
	"os"
	"regexp"
	"strconv"
	"time"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	buildappstudiov1alpha1 "github.com/redhat-appstudio/build-service/api/v1alpha1"
	l "github.com/redhat-appstudio/build-service/pkg/logs"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	RenovateConfigName          = "renovate-config"
	RenovateImageEnvName        = "RENOVATE_IMAGE"
	DefaultRenovateImageUrl     = "quay.io/redhat-appstudio/renovate:v37.74.1"
	DefaultRenovateMatchPattern = "^quay.io/redhat-appstudio-tekton-catalog/"
	RenovateMatchPatternEnvName = "RENOVATE_PATTERN"
	TimeToLiveOfJob             = 24 * time.Hour
	NextReconcile               = 1 * time.Hour
	InstallationsPerJob         = 20
	InstallationsPerJobEnvName  = "RENOVATE_INSTALLATIONS_PER_JOB"
	InternalDefaultBranch       = "$DEFAULTBRANCH"
)

// GitTektonResourcesRenovater watches AppStudio BuildPipelineSelector object in order to update
// existing .tekton directories.
type GitTektonResourcesRenovater struct {
	Client        client.Client
	Scheme        *runtime.Scheme
	EventRecorder record.EventRecorder
}

type installationStruct struct {
	id           int
	token        string
	repositories []renovateRepository
}

type renovateRepository struct {
	Repository   string   `json:"repository"`
	BaseBranches []string `json:"baseBranches,omitempty"`
}

// SetupWithManager sets up the controller with the Manager.
func (r *GitTektonResourcesRenovater) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&buildappstudiov1alpha1.BuildPipelineSelector{}, builder.WithPredicates(predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetNamespace() == buildServiceNamespaceName && e.Object.GetName() == buildPipelineSelectorResourceName
		},
		DeleteFunc: func(event.DeleteEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectNew.GetNamespace() == buildServiceNamespaceName && e.ObjectNew.GetName() == buildPipelineSelectorResourceName
		},
		GenericFunc: func(event.GenericEvent) bool {
			return false
		},
	})).Complete(r)
}

// Set Role for managing jobs/configmaps/secrets in the controller namespace

// +kubebuilder:rbac:namespace=system,groups=batch,resources=jobs,verbs=create;get;list;watch;delete;deletecollection
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;patch;update;delete;deletecollection
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;patch;update;delete;deletecollection

// +kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list

func (r *GitTektonResourcesRenovater) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("GitTektonResourcesRenovator")
	ctx = ctrllog.IntoContext(ctx, log)

	// Get Components
	componentList := &appstudiov1alpha1.ComponentList{}
	if err := r.Client.List(ctx, componentList, &client.ListOptions{}); err != nil {
		log.Error(err, "failed to list Components", l.Action, l.ActionView)
		return ctrl.Result{}, err
	}

	slug, installationsToUpdate, err := GetAllGithubInstallations(ctx, r.Client, r.EventRecorder, componentList.Items)
	if err != nil || slug == "" {
		return ctrl.Result{}, err
	}

	// Generate renovate jobs. Limit processed installations per job.
	var installationPerJobInt int
	installationPerJobStr := os.Getenv(InstallationsPerJobEnvName)
	if regexp.MustCompile(`^\d{1,2}$`).MatchString(installationPerJobStr) {
		installationPerJobInt, _ = strconv.Atoi(installationPerJobStr)
		if installationPerJobInt == 0 {
			installationPerJobInt = InstallationsPerJob
		}
	} else {
		installationPerJobInt = InstallationsPerJob
	}
	for i := 0; i < len(installationsToUpdate); i += installationPerJobInt {
		end := i + installationPerJobInt

		if end > len(installationsToUpdate) {
			end = len(installationsToUpdate)
		}
		err = CreateRenovaterJob(ctx, r.Client, r.Scheme, installationsToUpdate[i:end], slug, false, generateConfigJS, nil)
		if err != nil {
			log.Error(err, "failed to create a job", l.Action, l.ActionAdd)
		}
	}

	return ctrl.Result{RequeueAfter: NextReconcile}, nil
}

func generateConfigJS(slug string, repositories []renovateRepository, _ interface{}) (string, error) {
	repositoriesData, _ := json.Marshal(repositories)
	template := `
	module.exports = {
		platform: "github",
		username: "%s[bot]",
		gitAuthor:"%s <123456+%s[bot]@users.noreply.github.com>",
		onboarding: false,
		requireConfig: "ignored",
		enabledManagers: ["tekton"],
		repositories: %s,
		tekton: {
			fileMatch: ["\\.yaml$", "\\.yml$"],
			includePaths: [".tekton/**"],
			packageRules: [
			  {
				matchPackagePatterns: ["*"],
				enabled: false
			  },
			  {
				matchPackagePatterns: ["%s"],
				matchDepPatterns: ["%s"],
				groupName: "RHTAP references",
				branchName: "konflux/references/{{baseBranch}}",
				commitMessageExtra: "",
				commitMessageTopic: "RHTAP references",
				semanticCommits: "enabled",
				prFooter: "To execute skipped test pipelines write comment ` + "`/ok-to-test`" + `",
				prBodyColumns: ["Package", "Change", "Notes"],
				prBodyDefinitions: { "Notes": "{{#if (or (containsString updateType 'minor') (containsString updateType 'major'))}}:warning:[migration](https://github.com/redhat-appstudio/build-definitions/blob/main/task/{{{replace '%stask-' '' packageName}}}/{{{newVersion}}}/MIGRATION.md):warning:{{/if}}" },
				prBodyTemplate: "{{{header}}}{{{table}}}{{{notes}}}{{{changelogs}}}{{{footer}}}",
				recreateWhen: "always",
				rebaseWhen: "behind-base-branch",
				enabled: true
			  }
			]
		},
		forkProcessing: "enabled",
		dependencyDashboard: false
	}
	`
	renovatePattern := os.Getenv(RenovateMatchPatternEnvName)
	if renovatePattern == "" {
		renovatePattern = DefaultRenovateMatchPattern
	}
	return fmt.Sprintf(template, slug, slug, slug, repositoriesData, renovatePattern, renovatePattern, renovatePattern), nil
}
