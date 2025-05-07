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
	imgcv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

const (
	BuildPipelineClusterRoleName = "appstudio-pipelines-runner"
	buildPipelineRoleBindingName = "konflux-pipelines-runner-bindings"

	commonBuildSecretLabelName = "build.appstudio.openshift.io/common-secret"

	imageRepositoryComponentLabelName = "appstudio.redhat.com/component"

	serviceAccountMigrationAnnotationName = "build.appstudio.openshift.io/sa-migration"
)

// getBuildPipelineServiceAccountName returns name of dedicated Service Account
// that should be used for build pipelines of the given Component.
func getBuildPipelineServiceAccountName(component *appstudiov1alpha1.Component) string {
	return getBuildPipelineServiceAccountNameByComponentName(component.GetName())
}
func getBuildPipelineServiceAccountNameByComponentName(componentName string) string {
	return "build-pipeline-" + componentName
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

	if err := r.ensureNudgingPullSecrets(ctx, component); err != nil {
		log.Error(err, "failed to ensure nudge secrets linked")
		return err
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

// ensureNudgingPullSecrets makes sure that Component build pipeline Service Account
// has pull secrets for image repositories of Components that nudge current Component linked.
// Note, they must be linked into Secrets section (not imagePullSecrets).
func (r *ComponentBuildReconciler) ensureNudgingPullSecrets(ctx context.Context, component *appstudiov1alpha1.Component) error {
	buildPipelineServiceAccountName := getBuildPipelineServiceAccountName(component)
	log := ctrllog.FromContext(ctx).WithValues("ServiceAccountName", buildPipelineServiceAccountName)

	allComponentsInNamespaceList := &appstudiov1alpha1.ComponentList{}
	if err := r.Client.List(ctx, allComponentsInNamespaceList, client.InNamespace(component.Namespace)); err != nil {
		log.Error(err, "failed to list all Components in namespace", l.Action, l.ActionView)
		return err
	}

	// TODO use Component.Status.BuildNudgedBy when KONFLUX-6841 is resolved and the data in status will be reliable.
	// Find all Components that nudge current one.
	nudgingComponents := []string{}
	for _, c := range allComponentsInNamespaceList.Items {
		for _, componentNameToNudge := range c.Spec.BuildNudgesRef {
			if componentNameToNudge == component.Name {
				nudgingComponents = append(nudgingComponents, c.Name)
				break
			}
		}
	}

	if len(nudgingComponents) == 0 {
		// No components that nudge current one, nothing to link.
		return nil
	}

	buildPipelineServiceAccount := &corev1.ServiceAccount{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: component.Namespace}, buildPipelineServiceAccount); err != nil {
		log.Error(err, "failed to get build pipeline Service Account", l.Action, l.ActionView)
		return err
	}

	isServiceAccountUpdated := false
	for _, componentName := range nudgingComponents {
		imageRepository, err := r.getComponentImageRepository(ctx, componentName, component.Namespace)
		if err != nil {
			log.Error(err, "failed to get Image Repository for Component", "Component", componentName, l.Action, l.ActionView)
			return err
		}
		if imageRepository == nil {
			// The Component doesn't have related Image Repository object.
			continue
		}
		pullSecretName := imageRepository.Status.Credentials.PullSecretName
		if !isSaSecretLinked(buildPipelineServiceAccount, pullSecretName, false) {
			buildPipelineServiceAccount.Secrets = append(buildPipelineServiceAccount.Secrets,
				corev1.ObjectReference{Name: pullSecretName, Namespace: buildPipelineServiceAccount.Namespace})
			isServiceAccountUpdated = true
		}
	}
	if isServiceAccountUpdated {
		if err := r.Client.Update(ctx, buildPipelineServiceAccount); err != nil {
			log.Error(err, "failed to update build pipeline Service Account", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("Updated Service Account pull secrets")
	}

	return nil
}

// cleanUpNudgingPullSecrets removes the current Component pull secret from the linked secrets list
// of the nudged Components build pipeline Service Account.
func (r *ComponentBuildReconciler) cleanUpNudgingPullSecrets(ctx context.Context, component *appstudiov1alpha1.Component) error {
	if len(component.Spec.BuildNudgesRef) == 0 {
		// No nudge relations, nothing to do.
		return nil
	}

	log := ctrllog.FromContext(ctx).WithName("cleanUpNudgingPullSecrets")

	imageRepository, err := r.getComponentImageRepository(ctx, component.Name, component.Namespace)
	if err != nil {
		log.Error(err, "failed to get Image Repository", l.Action, l.ActionView)
		return err
	}
	if imageRepository == nil {
		// The Component doesn't have related Image Repository object.
		return nil
	}
	pullSecretName := imageRepository.Status.Credentials.PullSecretName

	for _, nudgedComponentName := range component.Spec.BuildNudgesRef {
		nudgedComponentBuildPipelineServiceAccountName := getBuildPipelineServiceAccountNameByComponentName(nudgedComponentName)
		nudgedComponentBuildPipelineServiceAccount := &corev1.ServiceAccount{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: nudgedComponentBuildPipelineServiceAccountName, Namespace: component.Namespace}, nudgedComponentBuildPipelineServiceAccount); err != nil {
			log.Error(err, "failed to get build pipeline Service Account for Component", "Component", nudgedComponentName, l.Action, l.ActionView)
			return err
		}

		pullSecretIndex := getSecretReferenceIndex(nudgedComponentBuildPipelineServiceAccount, pullSecretName, false)
		if pullSecretIndex == -1 {
			// The secret is not linked to the nudged Component build pipeline Service Account.
			continue
		}

		oldSecrets := nudgedComponentBuildPipelineServiceAccount.Secrets
		newSecrets := append(oldSecrets[:pullSecretIndex], oldSecrets[pullSecretIndex+1:]...)
		nudgedComponentBuildPipelineServiceAccount.Secrets = newSecrets

		if err := r.Client.Update(ctx, nudgedComponentBuildPipelineServiceAccount); err != nil {
			log.Error(err, "failed to remove pull secret link from build pipeline Service Account for Component", "Component", nudgedComponentName, l.Action, l.ActionUpdate)
			return err
		}
	}

	return nil
}

// getComponentImageRepository retreives related to the given Component Image Repository.
// Returns nil if the Component doesn't have related Image Repository.
func (r *ComponentBuildReconciler) getComponentImageRepository(ctx context.Context, componentName, namespace string) (*imgcv1alpha1.ImageRepository, error) {
	log := ctrllog.FromContext(ctx)

	imageRepositoryList := &imgcv1alpha1.ImageRepositoryList{}
	componentImageRepositoryRequirement, err := labels.NewRequirement(imageRepositoryComponentLabelName, selection.Equals, []string{componentName})
	if err != nil {
		return nil, err
	}
	componentImageRepositorySelector := labels.NewSelector().Add(*componentImageRepositoryRequirement)
	componentImageRepositoryListOptions := client.ListOptions{
		LabelSelector: componentImageRepositorySelector,
		Namespace:     namespace,
	}
	if err := r.Client.List(ctx, imageRepositoryList, &componentImageRepositoryListOptions); err != nil {
		log.Error(err, "failed to list Component Image Repository")
		return nil, err
	}
	if len(imageRepositoryList.Items) > 0 {
		// Each Component can have only one Image Repository
		return &imageRepositoryList.Items[0], nil
	}
	return nil, nil
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

// getSecretReferenceIndex returns index of secret reference in the given Service Account.
// Returns -1 if the secret is not linked.
func getSecretReferenceIndex(serviceAccount *corev1.ServiceAccount, secretName string, isPull bool) int {
	if isPull {
		for i, pullSecretObject := range serviceAccount.ImagePullSecrets {
			if pullSecretObject.Name == secretName {
				return i
			}
		}
	} else {
		for i, secretObject := range serviceAccount.Secrets {
			if secretObject.Name == secretName {
				return i
			}
		}
	}
	return -1
}

func isSaSecretLinked(serviceAccount *corev1.ServiceAccount, secretName string, isPull bool) bool {
	return getSecretReferenceIndex(serviceAccount, secretName, isPull) != -1
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
			log.Info("skipping linked secret migration due to missing appstudio-pipeline Service Account")
			return nil
		}
		return err
	}

	imageRepositoryList := &imgcv1alpha1.ImageRepositoryList{}
	if err := r.Client.List(ctx, imageRepositoryList, client.InNamespace(component.Namespace)); err != nil {
		log.Error(err, "failed to list ImageRepositories in namespace", l.Action, l.ActionView)
		return err
	}

	return LinkCommonAppstudioPipelineSecretsToNewServiceAccount(ctx, r.Client, component, appstudioPipelineServiceAccount, imageRepositoryList.Items)
}

// LinkCommonAppstudioPipelineSecretsToNewServiceAccount links all secrets from appstudio-pipeline Service Account
// except image repository secrets to the new dedicated to build Service Account.
func LinkCommonAppstudioPipelineSecretsToNewServiceAccount(ctx context.Context, c client.Client, component *appstudiov1alpha1.Component, appstudioPipelineServiceAccount *corev1.ServiceAccount, imageRepositories []imgcv1alpha1.ImageRepository) error {
	log := ctrllog.FromContext(ctx)

	componentBuildServiceAccount := &corev1.ServiceAccount{}
	if err := c.Get(ctx, types.NamespacedName{Name: getBuildPipelineServiceAccountName(component), Namespace: component.Namespace}, componentBuildServiceAccount); err != nil {
		log.Error(err, "failed to get component build Service Account")
		return err
	}

	isComponentServiceAccountEdited := migrateLinkedSecrets(appstudioPipelineServiceAccount, componentBuildServiceAccount, imageRepositories)
	if isComponentServiceAccountEdited {
		if err := c.Update(ctx, componentBuildServiceAccount); err != nil {
			log.Error(err, "failed to update build Service Account after attempt of secrets migration")
			return err
		}
		log.Info("Migrated common secrets to the new Service Account")
	}

	return nil
}

func migrateLinkedSecrets(appstudioPipelineServiceAccount, componentBuildServiceAccount *corev1.ServiceAccount, imageRepositories []imgcv1alpha1.ImageRepository) bool {
	isEdited := false

	// secrets
	for _, secretObject := range appstudioPipelineServiceAccount.Secrets {
		secretName := secretObject.Name
		if strings.HasPrefix(secretName, "appstudio-pipeline-dockercfg-") {
			// Skip dockerconfig of "appstudio-pipeline" Service Account
			continue
		}
		if isSaSecretLinked(componentBuildServiceAccount, secretName, false) {
			continue
		}
		if isImageRepositorySecret(secretName) && isComponentImageRepositorySecret(secretName, imageRepositories) {
			continue
		}
		componentBuildServiceAccount.Secrets = append(componentBuildServiceAccount.Secrets, corev1.ObjectReference{Name: secretName})
		isEdited = true
	}

	// pull secrets
	for _, pullSecretObject := range appstudioPipelineServiceAccount.ImagePullSecrets {
		pullSecretName := pullSecretObject.Name
		if strings.HasPrefix(pullSecretName, "appstudio-pipeline-dockercfg-") {
			// Skip dockerconfig of "appstudio-pipeline" Service Account
			continue
		}
		if isSaSecretLinked(componentBuildServiceAccount, pullSecretName, true) {
			continue
		}
		if isImageRepositorySecret(pullSecretName) && isComponentImageRepositorySecret(pullSecretName, imageRepositories) {
			continue
		}
		componentBuildServiceAccount.ImagePullSecrets = append(componentBuildServiceAccount.ImagePullSecrets, corev1.LocalObjectReference{Name: pullSecretName})
		isEdited = true
	}

	// Remove dockerconfig secrets of "appstudio-pipeline" Service Account
	for secretIndex, secretObject := range componentBuildServiceAccount.Secrets {
		secretName := secretObject.Name
		if strings.HasPrefix(secretName, "appstudio-pipeline-dockercfg-") {
			oldSecrets := componentBuildServiceAccount.Secrets
			newSecrets := append(oldSecrets[:secretIndex], oldSecrets[secretIndex+1:]...)
			componentBuildServiceAccount.Secrets = newSecrets
			isEdited = true
			break
		}
	}
	for pullSecretIndex, pullSecretObject := range componentBuildServiceAccount.ImagePullSecrets {
		secretName := pullSecretObject.Name
		if strings.HasPrefix(secretName, "appstudio-pipeline-dockercfg-") {
			oldPullSecrets := componentBuildServiceAccount.ImagePullSecrets
			newPullSecrets := append(oldPullSecrets[:pullSecretIndex], oldPullSecrets[pullSecretIndex+1:]...)
			componentBuildServiceAccount.ImagePullSecrets = newPullSecrets
			isEdited = true
			break
		}
	}

	return isEdited
}

func isImageRepositorySecret(secretName string) bool {
	return strings.HasPrefix(secretName, "imagerepository") || strings.HasSuffix(secretName, "image-push") || strings.HasSuffix(secretName, "image-pull")
}

// isComponentImageRepositorySecret returns true if given secret is related to an ImageRepository and
// the ImageRepository belongs to (i.e. owned by) a Component.
func isComponentImageRepositorySecret(secretName string, imageRepositories []imgcv1alpha1.ImageRepository) bool {
	for _, imageRepository := range imageRepositories {
		if imageRepository.Status.Credentials.PushSecretName == secretName || imageRepository.Status.Credentials.PullSecretName == secretName {
			for _, owner := range imageRepository.OwnerReferences {
				if owner.Kind == "Component" {
					return true
				}
			}
			// Handle the situation when both the Component and the Image Repository were created at the same time
			// and Image Controller operator hasn't yet set the owner reference.
			if imageRepository.Labels[ApplicationNameLabelName] != "" && imageRepository.Labels[ComponentNameLabelName] != "" {
				return true
			}
			return false
		}
	}
	return false
}

const saMigrationMergeRequestDescription = `
## Build pipeline Service Account migration

This PR changes Service Account used by build pipeline from "appstudio-pipeline" to dedicated to the Component Service Account.
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
	defaultBranch, err := gitClient.GetDefaultBranchWithChecks(repoUrl)
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
		if !errors.IsNotFound(err) && !strings.Contains(err.Error(), "not found") {
			return err
		}
		pushPipelineDefinitionFound = false
	}
	pullPipelineDefinitionFound := true
	var pullPipelineContent []byte
	pullPipelineContent, err = gitClient.DownloadFileContent(repoUrl, baseBranch, pullPipelinePath)
	if err != nil {
		if !errors.IsNotFound(err) && !strings.Contains(err.Error(), "not found") {
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
