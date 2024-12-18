/*
Copyright 2021-2024 Red Hat, Inc.

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
	"strings"

	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	. "github.com/konflux-ci/build-service/pkg/common"
	l "github.com/konflux-ci/build-service/pkg/logs"
)

// findPaCRepositoryForComponent searches for existing matching PaC repository object for given component.
// The search makes sense only in the same namespace.
func (r *ComponentBuildReconciler) findPaCRepositoryForComponent(ctx context.Context, component *appstudiov1alpha1.Component) (*pacv1alpha1.Repository, error) {
	log := ctrllog.FromContext(ctx)

	pacRepositoriesList := &pacv1alpha1.RepositoryList{}
	err := r.Client.List(ctx, pacRepositoriesList, &client.ListOptions{Namespace: component.Namespace})
	if err != nil {
		log.Error(err, "failed to list PaC repositories")
		return nil, err
	}

	gitUrl := strings.TrimSuffix(strings.TrimSuffix(component.Spec.Source.GitSource.URL, ".git"), "/")
	for _, pacRepository := range pacRepositoriesList.Items {
		if pacRepository.Spec.URL == gitUrl {
			return &pacRepository, nil
		}
	}
	return nil, nil
}

func (r *ComponentBuildReconciler) ensurePaCRepository(ctx context.Context, component *appstudiov1alpha1.Component, pacConfig *corev1.Secret) error {
	log := ctrllog.FromContext(ctx)

	// Check multi component git repository scenario.
	// It's not possible to determine multi component git repository scenario by context directory field,
	// therefore it's required to do the check for all components.
	// For example, there are several dockerfiles in the same git repository
	// and each of them builds separate component from the common codebase.
	// Another scenario is component per branch.
	repository, err := r.findPaCRepositoryForComponent(ctx, component)
	if err != nil {
		return err
	}
	if repository != nil {
		pacRepositoryOwnersNumber := len(repository.OwnerReferences)
		if err := controllerutil.SetOwnerReference(component, repository, r.Scheme); err != nil {
			log.Error(err, "failed to add owner reference to existing PaC repository", "PaCRepositoryName", repository.Name)
			return err
		}
		if len(repository.OwnerReferences) > pacRepositoryOwnersNumber {
			if err := r.Client.Update(ctx, repository); err != nil {
				log.Error(err, "failed to update existing PaC repository with component owner reference", "PaCRepositoryName", repository.Name)
				return err
			}
			log.Info("Added current component to owners of the PaC repository", "PaCRepositoryName", repository.Name, l.Action, l.ActionUpdate)
		} else {
			log.Info("Using existing PaC Repository object for the component", "PaCRepositoryName", repository.Name)
		}
		return nil
	}

	// This is the first Component that does PaC provision for the git repository
	repository, err = generatePACRepository(*component, pacConfig)
	if err != nil {
		return err
	}

	ns, err := r.getNamespace(ctx, component.GetNamespace())
	if err != nil {
		log.Error(err, "failed to get the component namespace for setting custom parameter.")
		return err
	}
	if val, ok := ns.Labels[appstudioWorkspaceNameLabel]; ok {
		pacRepoAddParamWorkspaceName(repository, val)
	}

	existingRepository := &pacv1alpha1.Repository{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: repository.Name, Namespace: repository.Namespace}, existingRepository)
	if err == nil {
		gitUrl := strings.TrimSuffix(strings.TrimSuffix(component.Spec.Source.GitSource.URL, ".git"), "/")

		if existingRepository.Spec.URL == gitUrl {
			return nil
		}
		// repository with the same name exists but with different git URL, add random string to repository name
		repository.ObjectMeta.Name = fmt.Sprintf("%s-%s", repository.ObjectMeta.Name, RandomString(5))
	}

	// create repository if not found or when found but with different URL
	if (err != nil && errors.IsNotFound(err)) || err == nil {
		if err := controllerutil.SetOwnerReference(component, repository, r.Scheme); err != nil {
			return err
		}
		if err := r.Client.Create(ctx, repository); err != nil {
			if strings.Contains(err.Error(), "repository already exist") {
				// PaC admission webhook denied creation of the PaC repository,
				// because PaC repository object that references the same git repository already exists.
				log.Info("An attempt to create second PaC Repository for the same git repository", "GitRepository", repository.Spec.URL, l.Action, l.ActionAdd, l.Audit, "true")
				return boerrors.NewBuildOpError(boerrors.EPaCDuplicateRepository, err)
			}

			if strings.Contains(err.Error(), "denied the request: failed to validate") {
				// PaC admission webhook denied creation of the PaC repository,
				// because PaC repository object references not allowed git repository url.
				log.Info("An attempt to create PaC Repository for not allowed repository url", "GitRepository", repository.Spec.URL, l.Action, l.ActionAdd, l.Audit, "true")
				return boerrors.NewBuildOpError(boerrors.EPaCNotAllowedRepositoryUrl, err)
			}

			log.Error(err, "failed to create Component PaC repository object", l.Action, l.ActionAdd)
			return err
		}
		log.Info("Created PaC Repository object for the component", "RepositoryName", repository.ObjectMeta.Name)

	} else {
		log.Error(err, "failed to get Component PaC repository object", l.Action, l.ActionView)
		return err
	}

	return nil
}

func (r *ComponentBuildReconciler) getNamespace(ctx context.Context, name string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: name}, ns)
	if err == nil {
		return ns, nil
	} else {
		return nil, err
	}
}

// pacRepoAddParamWorkspaceName adds custom parameter workspace name to a PaC repository.
// Existing parameter will be overridden.
func pacRepoAddParamWorkspaceName(repository *pacv1alpha1.Repository, workspaceName string) {
	var params []pacv1alpha1.Params
	// Before pipelines-as-code gets upgraded for application-service to the
	// version supporting custom parameters, this check must be taken.
	if repository.Spec.Params == nil {
		params = make([]pacv1alpha1.Params, 0)
	} else {
		params = *repository.Spec.Params
	}
	found := -1
	for i, param := range params {
		if param.Name == pacCustomParamAppstudioWorkspace {
			found = i
			break
		}
	}
	workspaceParam := pacv1alpha1.Params{
		Name:  pacCustomParamAppstudioWorkspace,
		Value: workspaceName,
	}
	if found >= 0 {
		params[found] = workspaceParam
	} else {
		params = append(params, workspaceParam)
	}
	repository.Spec.Params = &params
}

// generatePACRepository creates configuration of Pipelines as Code repository object.
func generatePACRepository(component appstudiov1alpha1.Component, config *corev1.Secret) (*pacv1alpha1.Repository, error) {
	gitProvider, err := getGitProvider(component)
	if err != nil {
		return nil, err
	}

	isAppUsed := IsPaCApplicationConfigured(gitProvider, config.Data)

	var gitProviderConfig *pacv1alpha1.GitProvider = nil
	if !isAppUsed {
		// Webhook is used
		gitProviderConfig = &pacv1alpha1.GitProvider{
			Secret: &pacv1alpha1.Secret{
				Name: config.Name,
				Key:  corev1.BasicAuthPasswordKey, // basic-auth secret type expected
			},
			WebhookSecret: &pacv1alpha1.Secret{
				Name: pipelinesAsCodeWebhooksSecretName,
				Key:  getWebhookSecretKeyForComponent(component),
			},
		}

		// gitProviderType is needed for incoming webhook handling
		gitProviderType := gitProvider
		if gitProvider == "bitbucket" {
			// https://pipelinesascode.com/docs/guide/incoming_webhook/#incoming-webhook-url
			gitProviderType = "bitbucket-cloud"
		}
		gitProviderConfig.Type = gitProviderType

		var gitProviderUrl string
		if providerUrlFromAnnotation, configured := component.Annotations[GitProviderAnnotationURL]; configured {
			// Use git provider URL provided via the annotation.
			// Make sure that the url has protocol as it's required.
			if !strings.Contains(providerUrlFromAnnotation, "://") {
				gitProviderUrl = "https://" + providerUrlFromAnnotation
			} else {
				gitProviderUrl = providerUrlFromAnnotation
			}
		} else {
			// Get git provider URL from source URL.
			u, err := url.Parse(component.Spec.Source.GitSource.URL)
			if err != nil {
				return nil, err
			}
			gitProviderUrl = u.Scheme + "://" + u.Host
		}
		gitProviderConfig.URL = gitProviderUrl
	}

	repository := &pacv1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Repository",
			APIVersion: "pipelinesascode.tekton.dev/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      component.Name,
			Namespace: component.Namespace,
		},
		Spec: pacv1alpha1.RepositorySpec{
			URL:         strings.TrimSuffix(strings.TrimSuffix(component.Spec.Source.GitSource.URL, ".git"), "/"),
			GitProvider: gitProviderConfig,
		},
	}

	return repository, nil
}

// cleanupPaCRepositoryIncomingsAndSecret is cleaning up incomings in Repository
// for unprovisioned component, and also removes incoming secret when no longer required
func (r *ComponentBuildReconciler) cleanupPaCRepositoryIncomingsAndSecret(ctx context.Context, component *appstudiov1alpha1.Component, baseBranch string) error {
	log := ctrllog.FromContext(ctx)

	// check if more components are using same repo with PaC enabled for incomings removal from repository
	incomingsRepoTargetBranchCount := 0
	incomingsRepoAllBranchesCount := 0
	componentList := &appstudiov1alpha1.ComponentList{}
	if err := r.Client.List(ctx, componentList, &client.ListOptions{Namespace: component.Namespace}); err != nil {
		log.Error(err, "failed to list Components", l.Action, l.ActionView)
		return err
	}
	buildStatus := &BuildStatus{}
	for _, comp := range componentList.Items {
		if comp.Spec.Source.GitSource.URL == component.Spec.Source.GitSource.URL {
			buildStatus = readBuildStatus(component)
			if buildStatus.PaC != nil && buildStatus.PaC.State == "enabled" {
				incomingsRepoAllBranchesCount += 1

				// revision can be empty and then use default branch
				if comp.Spec.Source.GitSource.Revision == component.Spec.Source.GitSource.Revision || comp.Spec.Source.GitSource.Revision == baseBranch {
					incomingsRepoTargetBranchCount += 1
				}
			}
		}
	}

	repository, err := r.findPaCRepositoryForComponent(ctx, component)
	if err != nil {
		return err
	}

	// repository is used to construct incoming secret name
	if repository == nil {
		return nil
	}

	incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)
	incomingUpdated := false
	// update first in case there is multiple incoming entries, and it will be converted to incomings with just 1 entry
	_ = updateIncoming(repository, incomingSecretName, pacIncomingSecretKey, baseBranch)

	if len((*repository.Spec.Incomings)[0].Targets) > 1 {
		// incoming contains target from the current component only
		if slices.Contains((*repository.Spec.Incomings)[0].Targets, baseBranch) && incomingsRepoTargetBranchCount <= 1 {
			newTargets := []string{}
			for _, target := range (*repository.Spec.Incomings)[0].Targets {
				if target != baseBranch {
					newTargets = append(newTargets, target)
				}
			}
			(*repository.Spec.Incomings)[0].Targets = newTargets
			incomingUpdated = true
		}
		// remove secret from incomings if just current component is using incomings in repository
		if incomingsRepoAllBranchesCount <= 1 && incomingsRepoTargetBranchCount <= 1 {
			(*repository.Spec.Incomings)[0].Secret = pacv1alpha1.Secret{}
			incomingUpdated = true
		}

	} else {
		// incomings has just 1 target and that target is from the current component only
		if (*repository.Spec.Incomings)[0].Targets[0] == baseBranch && incomingsRepoTargetBranchCount <= 1 {
			repository.Spec.Incomings = nil
			incomingUpdated = true
		}
	}

	if incomingUpdated {
		if err := r.Client.Update(ctx, repository); err != nil {
			log.Error(err, "failed to update existing PaC repository with incomings", "PaCRepositoryName", repository.Name)
			return err
		}
		log.Info("Removed incomings from the PaC repository", "PaCRepositoryName", repository.Name, l.Action, l.ActionUpdate)
	}

	// remove incoming secret if just current component is using incomings in repository
	if incomingsRepoAllBranchesCount <= 1 && incomingsRepoTargetBranchCount <= 1 {
		secret := &corev1.Secret{}
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}, secret); err != nil {
			if !errors.IsNotFound(err) {
				log.Error(err, "failed to get incoming secret", l.Action, l.ActionView)
				return err
			}
			log.Info("incoming secret doesn't exist anymore, removal isn't required")
		} else {
			if err := r.Client.Delete(ctx, secret); err != nil {
				if !errors.IsNotFound(err) {
					log.Error(err, "failed to remove incoming secret", l.Action, l.ActionView)
					return err
				}
			}
			log.Info("incoming secret removed")
		}
	}
	return nil
}

// updateIncoming updates incomings in repository, adds new incoming for provided branch with incoming secret
// if repository contains multiple incoming entries, it will merge them to one, and combine Targets and add incoming secret to incoming
// if repository contains one incoming entry, it will add new target and add incoming secret to incoming
// if repository doesn't have any incoming entry, it will add new incoming entry with target and add incoming secret to incoming
// Returns bool, indicating if incomings in repository was updated or not
func updateIncoming(repository *pacv1alpha1.Repository, incomingSecretName string, pacIncomingSecretKey string, targetBranch string) bool {
	foundSecretName := false
	foundTarget := false
	multiple_incomings := false
	all_targets := []string{}
	foundParam := false

	if repository.Spec.Incomings != nil {
		if len(*repository.Spec.Incomings) > 1 {
			multiple_incomings = true
		}

		for idx, key := range *repository.Spec.Incomings {
			if multiple_incomings { // for multiple incomings gather all targets
				for _, target := range key.Targets {
					all_targets = append(all_targets, target)
					if target == targetBranch {
						foundTarget = true
					}
				}
			} else { // for single incoming add target & secret if missing
				for _, target := range key.Targets {
					if target == targetBranch {
						foundTarget = true
						break
					}
				}

				for _, param := range key.Params {
					if param == "source_url" {
						foundParam = true
						break
					}
				}

				if !foundParam {
					(*repository.Spec.Incomings)[idx].Params = append((*repository.Spec.Incomings)[idx].Params, "source_url")
				}

				// add missing target branch
				if !foundTarget {
					(*repository.Spec.Incomings)[idx].Targets = append((*repository.Spec.Incomings)[idx].Targets, targetBranch)
				}

				if key.Secret.Name == incomingSecretName {
					foundSecretName = true
				} else {
					(*repository.Spec.Incomings)[idx].Secret = pacv1alpha1.Secret{Name: incomingSecretName, Key: pacIncomingSecretKey}
				}
			}
		}

		// combine multiple incomings into one and add secret
		if multiple_incomings {
			if !foundTarget {
				all_targets = append(all_targets, targetBranch)
			}
			incoming := []pacv1alpha1.Incoming{{Type: "webhook-url",
				Secret:  pacv1alpha1.Secret{Name: incomingSecretName, Key: pacIncomingSecretKey},
				Targets: all_targets,
				Params:  []string{"source_url"}}}
			repository.Spec.Incomings = &incoming
		}
	} else {
		// create incomings when missing
		incoming := []pacv1alpha1.Incoming{{Type: "webhook-url",
			Secret:  pacv1alpha1.Secret{Name: incomingSecretName, Key: pacIncomingSecretKey},
			Targets: []string{targetBranch},
			Params:  []string{"source_url"}}}
		repository.Spec.Incomings = &incoming
	}

	return multiple_incomings || !(foundSecretName && foundTarget) || !foundParam
}
