/*
Copyright 2025 Red Hat, Inc.

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

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	"github.com/konflux-ci/build-service/pkg/boerrors"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
	"github.com/konflux-ci/build-service/pkg/git/gitproviderfactory"
	l "github.com/konflux-ci/build-service/pkg/logs"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

const (
	BuildPipelineClusterRoleName = "appstudio-pipelines-runner"
	buildPipelineRoleBindingName = BuildPipelineClusterRoleName + "-bindings"

	commonBuildSecretLabelName = "build.appstudio.openshift.io/common-secret"

	serviceAccountMigrationAnnotationName = "build.appstudio.openshift.io/sa-migration"
)

// getBuildPipelineServiceAccountName returns name of dedicated Service Account
// that should be used for build pipelines of the given Component.
func getBuildPipelineServiceAccountName(component *appstudiov1alpha1.Component) string {
	return "build-pipeline-" + component.GetName()
}

// EnsureBuildPipelineServiceAccount checks if dedicated Service Account for Component build pipelines and
// corresponding Role Binding to appstudio-pipeline-runner Cluster Role exists.
// If a resource missing, it will be created.
func (r *ComponentBuildReconciler) EnsureBuildPipelineServiceAccount(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := ctrllog.FromContext(ctx).WithName("buildSA-provision")
	ctx = ctrllog.IntoContext(ctx, log)

	// Verify build pipeline Service Account
	buildPipelineServiceAccountName := getBuildPipelineServiceAccountName(component)
	buildPipelinesServiceAccount := &corev1.ServiceAccount{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: component.Namespace}, buildPipelinesServiceAccount); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("failed to read build pipeline Service Account %s in namespace %s", buildPipelineServiceAccountName, component.Namespace), l.Action, l.ActionView)
			return err
		}

		// Service Account for Component build pipelines doesn't exist.
		// Create Service Account for the build pipeline.
		buildPipelineSA := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      buildPipelineServiceAccountName,
				Namespace: component.Namespace,
			},
		}
		if err := controllerutil.SetOwnerReference(component, buildPipelineSA, r.Scheme); err != nil {
			log.Error(err, "failed to add owner reference to build pipeline Service Account")
			return err
		}
		if err := r.linkCommonSecretsToBuildPipelineServiceAccount(ctx, buildPipelineSA); err != nil {
			return err
		}
		if err := r.Client.Create(ctx, buildPipelineSA); err != nil {
			log.Error(err, fmt.Sprintf("failed to create Service Account %s in namespace %s", buildPipelineServiceAccountName, component.Namespace), l.Action, l.ActionAdd)
			return err
		}
	}

	if err := r.ensureBuildPipelineServiceAccountBinding(ctx, component); err != nil {
		log.Error(err, "failed to ensure role binding for build pipleine Service Account")
		return err
	}

	return nil
}

// linkCommonSecretsToBuildPipelineServiceAccount searches for common build Secrets in Component namespace
// marked with corresponding label and adds them into secrets section of the given Service Account.
func (r *ComponentBuildReconciler) linkCommonSecretsToBuildPipelineServiceAccount(ctx context.Context, serviceAccount *corev1.ServiceAccount) error {
	// Get all secrets with common for all Component label
	commonSecretsList := &corev1.SecretList{}

	commonBuildSecretRequirement, err := labels.NewRequirement(commonBuildSecretLabelName, selection.Equals, []string{"true"})
	if err != nil {
		return err
	}
	commonBuildSecretsSelector := labels.NewSelector().Add(*commonBuildSecretRequirement)
	commonBuildSecretsListOptions := client.ListOptions{
		LabelSelector: commonBuildSecretsSelector,
		Namespace:     serviceAccount.Namespace,
	}
	if err := r.Client.List(ctx, commonSecretsList, &commonBuildSecretsListOptions); err != nil {
		return err
	}

	// Add found secrets to the Service Account Secrets section
	for _, commonSecret := range commonSecretsList.Items {
		serviceAccount.Secrets = append(serviceAccount.Secrets, corev1.ObjectReference{Name: commonSecret.Name})
	}

	return nil
}

// ensureBuildPipelineServiceAccountBinding makes sure that a common for all build pipelines Service Accounts
// Role Binding exists and contains a subject record for the given component.
func (r *ComponentBuildReconciler) ensureBuildPipelineServiceAccountBinding(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := ctrllog.FromContext(ctx)

	buildPipelineServiceAccountName := getBuildPipelineServiceAccountName(component)

	// Verify Role Binding for build pipeline Service Account
	buildPipelinesRoleBinding := &rbacv1.RoleBinding{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: component.Namespace}, buildPipelinesRoleBinding); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "failed to read common build pipelines Role Binding", l.Action, l.ActionView)
			return err
		}

		// This is the first Component in the namespace being onboarded.
		// Create common Role Binding to appstudio-pipelines-runner Cluster Role for all build pipeline Service Accounts.
		// Add only one subject for the given Component.
		buildPipelinesRoleBinding = &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      buildPipelineRoleBindingName,
				Namespace: component.Namespace,
			},
			RoleRef: rbacv1.RoleRef{
				Kind:     "ClusterRole",
				Name:     BuildPipelineClusterRoleName,
				APIGroup: rbacv1.SchemeGroupVersion.Group,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      buildPipelineServiceAccountName,
					Namespace: component.Namespace,
				},
			},
		}
		if err := r.Client.Create(ctx, buildPipelinesRoleBinding); err != nil {
			// errors.IsNotFound(err) will always be false because appstudio-pipelines-runner Cluster Role exists.
			log.Error(err, "failed to create common build pipelines Role Binding", l.Action, l.ActionAdd)
			return err
		}
		return nil
	}

	// Common Role Binding to appstudio-pipelines-runner Cluster Role for all build pipeline Service Accounts exists.
	// Make sure it contains a subject record for the given Component.
	if getRoleBindingSaSubjectIndex(buildPipelinesRoleBinding, buildPipelineServiceAccountName) != -1 {
		// Up to date, nothing to do.
		return nil
	}
	// Add record for the Component build pipeline Service Account and update common Role Binding.
	buildPipelinesRoleBinding.Subjects = append(buildPipelinesRoleBinding.Subjects, rbacv1.Subject{
		Kind:      "ServiceAccount",
		Name:      buildPipelineServiceAccountName,
		Namespace: component.Namespace,
	})
	if err := r.Client.Update(ctx, buildPipelinesRoleBinding); err != nil {
		log.Error(err, "failed to add a subject to common build pipelines Role Binding", l.Action, l.ActionUpdate)
		return err
	}
	log.Info("Added Service Account to common build pipelines Role Binding", "ServiceAccountName", buildPipelineServiceAccountName, l.Action, l.ActionUpdate)

	return nil
}

// getRoleBindingSaSubjectIndex checks if given Role Binding grants permissions to a Service Account
// with the provided name and returns the subject record index.
// If there is no record for the Service Account, -1 is returned.
func getRoleBindingSaSubjectIndex(roleBinding *rbacv1.RoleBinding, serviceAccountName string) int {
	for i, subject := range roleBinding.Subjects {
		if subject.Kind == "ServiceAccount" && subject.Name == serviceAccountName {
			return i
		}
	}
	return -1
}

// removeBuildPipelineServiceAccountBinding deletes subject record for build pipleine Service Account
// of the given Component from the common build pipelines Role Binding.
// Deletes the common Role Binding if no subjects are left.
func (r *ComponentBuildReconciler) removeBuildPipelineServiceAccountBinding(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := ctrllog.FromContext(ctx)
	log.Info("requested to remove build pipeline Service Account binding")

	buildPipelinesRoleBinding := &rbacv1.RoleBinding{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: component.Namespace}, buildPipelinesRoleBinding); err != nil {
		if errors.IsNotFound(err) {
			// Nothing to do
			return nil
		}
		log.Error(err, "failed to read common build pipelines Role Binding", l.Action, l.ActionView)
		return err
	}

	subjectsNumberBefore := len(buildPipelinesRoleBinding.Subjects)

	buildPipelineServiceAccountName := getBuildPipelineServiceAccountName(component)
	subjectIndex := getRoleBindingSaSubjectIndex(buildPipelinesRoleBinding, buildPipelineServiceAccountName)
	if subjectIndex != -1 {
		if len(buildPipelinesRoleBinding.Subjects) == 1 {
			// Remove the last subject
			buildPipelinesRoleBinding.Subjects = nil
		} else {
			// Remove subject by its index
			oldSubjects := buildPipelinesRoleBinding.Subjects
			newSubjects := append(oldSubjects[:subjectIndex], oldSubjects[subjectIndex+1:]...)
			buildPipelinesRoleBinding.Subjects = newSubjects
		}
	}

	// This is nice to have action to clean up records for already deleted Service Accounts.
	// That might happen when a user creates a Component and deletes it before PaC provision.
	if len(buildPipelinesRoleBinding.Subjects) != 0 {
		saList := &corev1.ServiceAccountList{}
		if err := r.Client.List(ctx, saList, client.InNamespace(buildPipelinesRoleBinding.Namespace)); err == nil {
			buildPipelinesRoleBinding = removeInvalidServiceAccountSubjects(buildPipelinesRoleBinding, saList.Items)
		} else {
			// Do not break flow because of optional cleanup
			log.Error(err, "failed to list Service Accounts, skipping additional build pipeline Role Binding cleanup")
		}
	}

	if len(buildPipelinesRoleBinding.Subjects) != 0 {
		if len(buildPipelinesRoleBinding.Subjects) != subjectsNumberBefore {
			if err := r.Client.Update(ctx, buildPipelinesRoleBinding); err != nil {
				log.Error(err, "failed to remove subject from common build pipelines Role Binding")
				return err
			}
			log.Info("removed subject from common build pipelines Role Binding")
		}
	} else {
		// No subjects left, remove whole Role Binding
		if err := r.Client.Delete(ctx, buildPipelinesRoleBinding); err != nil {
			log.Error(err, "failed to delete common build pipelines Role Binding", l.Action, l.ActionDelete)
			return err
		}
		log.Info("deleted common build pipelines Role Binding")
	}

	return nil
}

// removeInvalidServiceAccountSubjects removes unexisting Service Accounts from the given Role Binding.
func removeInvalidServiceAccountSubjects(roleBinding *rbacv1.RoleBinding, existingSAs []corev1.ServiceAccount) *rbacv1.RoleBinding {
	var newSubjects []rbacv1.Subject = nil
	for _, subject := range roleBinding.Subjects {
		if subject.Kind == "ServiceAccount" {
			for _, sa := range existingSAs {
				if subject.Name == sa.Name {
					newSubjects = append(newSubjects, subject)
					break
				}
			}
		} else {
			newSubjects = append(newSubjects, subject)
		}
	}
	roleBinding.Subjects = newSubjects
	return roleBinding
}

//
// Migration from appstudio-pipeline Service Account to dedicated to build Service Account
//

func (r *ComponentBuildReconciler) performServiceAccountMigration(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := ctrllog.FromContext(ctx).WithName("buildSA-migration")
	ctx = ctrllog.IntoContext(ctx, log)

	if err := r.linkCommonAppstudioPipelineSecretsToNewServiceAccount(ctx, component); err != nil {
		log.Error(err, "failed to link common secret to new Service Account")
		return err
	}

	if err := r.setServiceAccountInPipelineDefinition(ctx, component); err != nil {
		log.Error(err, "failed to create migration PR")
		return err
	}

	return nil
}

func (r *ComponentBuildReconciler) linkCommonAppstudioPipelineSecretsToNewServiceAccount(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := ctrllog.FromContext(ctx)

	appstudioPipelineServiceAccount := &corev1.ServiceAccount{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: component.Namespace}, appstudioPipelineServiceAccount); err != nil {
		if errors.IsNotFound(err) {
			// Ignore not found error, assume the migration for the linked secrets has been done.
			log.Info("skipping linked secret migration")
			return nil
		}
		return err
	}
	return LinkCommonAppstudioPipelineSecretsToNewServiceAccount(ctx, r.Client, component, appstudioPipelineServiceAccount)
}

// LinkCommonSecretsToNewServiceAccount links all secrets from appstudio-pipeline Service Account
// except image repository secrets to the new dedicated to build Service Account.
func LinkCommonAppstudioPipelineSecretsToNewServiceAccount(ctx context.Context, c client.Client, component *appstudiov1alpha1.Component, appstudioPipelineServiceAccount *corev1.ServiceAccount) error {
	log := ctrllog.FromContext(ctx)

	componentBuildServiceAccount := &corev1.ServiceAccount{}
	if err := c.Get(ctx, types.NamespacedName{Name: getBuildPipelineServiceAccountName(component), Namespace: component.Namespace}, componentBuildServiceAccount); err != nil {
		log.Error(err, "failed to get component build Service Account")
		return err
	}

	isComponentServiceAccountEdited := migrateLinkedSecrets(appstudioPipelineServiceAccount, componentBuildServiceAccount)
	if isComponentServiceAccountEdited {
		if err := c.Update(ctx, componentBuildServiceAccount); err != nil {
			log.Error(err, "failed to update build Service Account after attempt of secrets migration")
			return err
		}
		log.Info("Migrated common secrets to the new Service Account")
	}

	return nil
}

func migrateLinkedSecrets(appstudioPipelineServiceAccount, componentBuildServiceAccount *corev1.ServiceAccount) bool {
	isEdited := false

	// secrets
	for _, secretObject := range appstudioPipelineServiceAccount.Secrets {
		secretName := secretObject.Name
		if !isImageRepositorySecret(secretName) && !isSaSecretLinked(componentBuildServiceAccount, secretName, false) {
			componentBuildServiceAccount.Secrets = append(componentBuildServiceAccount.Secrets, corev1.ObjectReference{Name: secretName})
			isEdited = true
		}
	}

	// pull secrets
	for _, pullSecretObject := range appstudioPipelineServiceAccount.ImagePullSecrets {
		pullSecretName := pullSecretObject.Name
		if !isImageRepositorySecret(pullSecretName) && !isSaSecretLinked(componentBuildServiceAccount, pullSecretName, true) {
			componentBuildServiceAccount.ImagePullSecrets = append(componentBuildServiceAccount.ImagePullSecrets, corev1.LocalObjectReference{Name: pullSecretName})
			isEdited = true
		}
	}

	return isEdited
}

func isImageRepositorySecret(secretName string) bool {
	return strings.HasPrefix(secretName, "imagerepository") || strings.HasSuffix(secretName, "image-push")
}

func isSaSecretLinked(serviceAccount *corev1.ServiceAccount, secretName string, isPull bool) bool {
	if isPull {
		for _, pullSecretObject := range serviceAccount.ImagePullSecrets {
			if pullSecretObject.Name == secretName {
				return true
			}
		}
	} else {
		for _, secretObject := range serviceAccount.Secrets {
			if secretObject.Name == secretName {
				return true
			}
		}
	}
	return false
}

const saMigrationMergeRequestDescription = `
## Build pipeline Service Account migration

This PR chnages Service Account used by build pipeline from "appstudio-pipeline" to dedicated to the Component Service Account.
Please merge the Service Account update to avoid broken builds when deprected "appstudio-pipeline" Service Account is removed.
`

// Need to create a PR that adds to every pipeline definition:
func (r *ComponentBuildReconciler) setServiceAccountInPipelineDefinition(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := ctrllog.FromContext(ctx)

	gitProvider, err := getGitProvider(*component)
	if err != nil {
		// There is no point to continue if git provider is not known.
		return err
	}

	pacSecret, err := r.lookupPaCSecret(ctx, component, gitProvider)
	if err != nil {
		log.Error(err, "error getting git provider credentials secret", l.Action, l.ActionView)
		// Cannot continue without accessing git provider credentials.
		return boerrors.NewBuildOpError(boerrors.EPaCSecretNotFound, err)
	}

	repoUrl := getGitRepoUrl(*component)

	gitClient, err := gitproviderfactory.CreateGitClient(gitproviderfactory.GitClientConfig{
		PacSecretData:             pacSecret.Data,
		GitProvider:               gitProvider,
		RepoUrl:                   repoUrl,
		IsAppInstallationExpected: true,
	})
	if err != nil {
		return err
	}

	// getting branch in advance just to test credentials
	defaultBranch, err := gitClient.GetDefaultBranch(repoUrl)
	if err != nil {
		return err
	}
	baseBranch := component.Spec.Source.GitSource.Revision
	if baseBranch == "" {
		baseBranch = defaultBranch
	}

	pushPipelinePath := getPipelineRunDefinitionFilePath(component, false)
	pullPipelinePath := getPipelineRunDefinitionFilePath(component, true)

	pushPipelineDefinitionFound := true
	var pushPipelineContent []byte
	pushPipelineContent, err = gitClient.DownloadFileContent(repoUrl, baseBranch, pushPipelinePath)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		pushPipelineDefinitionFound = false
	}
	pullPipelineDefinitionFound := true
	var pullPipelineContent []byte
	pullPipelineContent, err = gitClient.DownloadFileContent(repoUrl, baseBranch, pullPipelinePath)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		pullPipelineDefinitionFound = false
	}
	if !pushPipelineDefinitionFound && !pullPipelineDefinitionFound {
		// No pipeline definitions found
		log.Info("No pipeline definitions found to migrate")
		return nil
	}

	pipelineRunServiceAccountName := getBuildPipelineServiceAccountName(component)

	pushPipelineDefinitionUpdated := false
	if pushPipelineDefinitionFound {
		pushPipelineContent, pushPipelineDefinitionUpdated, err = addServiceAccountToPipelineRun(pushPipelineContent, pipelineRunServiceAccountName)
		if err != nil {
			log.Error(err, "failed to add Service Account to push pipeline definition")
			return err
		}
	}
	pullPipelineDefinitionUpdated := false
	if pullPipelineDefinitionFound {
		pullPipelineContent, pullPipelineDefinitionUpdated, err = addServiceAccountToPipelineRun(pullPipelineContent, pipelineRunServiceAccountName)
		if err != nil {
			log.Error(err, "failed to add Service Account to pull pipeline definition")
			return err
		}
	}
	if !pushPipelineDefinitionUpdated && !pullPipelineDefinitionUpdated {
		// Nothing to update, stop
		log.Info("User has already set a custom Service Account for their pipelines, skip migration")
		return nil
	}

	mrData := &gp.MergeRequestData{
		CommitMessage:  "Konflux build pipeline service account migration for " + component.Name,
		SignedOff:      true,
		BranchName:     fmt.Sprintf("%s%s", "konflux-sa-migration-", component.Name),
		BaseBranchName: baseBranch,
		Title:          "Konflux build pipeline service account migration",
		Text:           saMigrationMergeRequestDescription,
		AuthorName:     "konflux",
		AuthorEmail:    "konflux@no-reply.konflux-ci.dev",
		Files:          []gp.RepositoryFile{},
	}
	if pushPipelineDefinitionFound && pushPipelineDefinitionUpdated {
		mrData.Files = append(mrData.Files, gp.RepositoryFile{FullPath: pushPipelinePath, Content: pushPipelineContent})
	}
	if pullPipelineDefinitionFound && pullPipelineDefinitionUpdated {
		mrData.Files = append(mrData.Files, gp.RepositoryFile{FullPath: pullPipelinePath, Content: pullPipelineContent})
	}

	var webUrl string
	webUrl, err = gitClient.EnsurePaCMergeRequest(repoUrl, mrData)
	if err != nil {
		log.Error(err, "failed to create migration PR")
		return err
	}

	log.Info("Migration PR: " + webUrl)
	return nil
}

// addServiceAccountToPipelineRun specifies given service account to use in the pipeline.
// If a user specified Service Account different than "appstudio-pipeline", do not overwrite it.
func addServiceAccountToPipelineRun(pipelineRunBytes []byte, serviceAccountName string) ([]byte, bool, error) {
	buildPipelineData := &tektonapi.PipelineRun{}
	if err := yaml.Unmarshal(pipelineRunBytes, buildPipelineData); err != nil {
		return nil, false, err
	}

	if buildPipelineData.Spec.TaskRunTemplate.ServiceAccountName == "" || buildPipelineData.Spec.TaskRunTemplate.ServiceAccountName == buildPipelineServiceAccountName {
		// Update Service Account to the dedicated one.
		buildPipelineData.Spec.TaskRunTemplate.ServiceAccountName = serviceAccountName

		pipelineRun, err := yaml.Marshal(buildPipelineData)
		if err != nil {
			return nil, false, err
		}
		return pipelineRun, true, nil
	}

	// Service Account is already set by user, do not overwrite.
	return pipelineRunBytes, false, nil
}
