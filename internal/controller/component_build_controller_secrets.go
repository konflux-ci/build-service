/*
Copyright 2021-2025 Red Hat, Inc.

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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	"github.com/konflux-ci/build-service/pkg/common"
	"github.com/konflux-ci/build-service/pkg/git"
	l "github.com/konflux-ci/build-service/pkg/logs"
)

// ensureIncomingSecret is ensuring that incoming secret for PaC trigger exists
// if secret doesn't exists it will create it and also add repository as owner
// TODO remove newModel handling after only new model is used
// Returns:
// pointer to secret object
// bool which indicates if reconcile is required (which is required when we just created secret)
func (r *ComponentBuildReconciler) ensureIncomingSecret(ctx context.Context, component *compapiv1alpha1.Component, newModel bool) (*corev1.Secret, bool, error) {
	log := ctrllog.FromContext(ctx)

	repository, err := r.findPaCRepositoryForComponent(ctx, component, newModel)
	if err != nil {
		return nil, false, fmt.Errorf("failed to find PaC repository for component: %w", err)
	}

	incomingSecretName := getPaCIncomingSecretName(repository.Name)
	incomingSecretPassword := generatePaCWebhookSecretString()
	incomingSecretData := map[string]string{
		pacIncomingSecretKey: incomingSecretPassword,
	}

	secret := corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}, &secret); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "failed to get incoming secret", l.Action, l.ActionView)
			return nil, false, fmt.Errorf("failed to get incoming secret %s: %w", incomingSecretName, err)
		}
		// Create incoming secret
		secret = corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      incomingSecretName,
				Namespace: component.Namespace,
			},
			Type:       corev1.SecretTypeOpaque,
			StringData: incomingSecretData,
		}

		if err := controllerutil.SetOwnerReference(repository, &secret, r.Scheme); err != nil {
			log.Error(err, "failed to set owner for incoming secret")
			return nil, false, fmt.Errorf("failed to set owner reference on incoming secret: %w", err)
		}

		if err := r.Client.Create(ctx, &secret); err != nil {
			log.Error(err, "failed to create incoming secret", l.Action, l.ActionAdd)
			return nil, false, fmt.Errorf("failed to create incoming secret %s: %w", incomingSecretName, err)
		}

		log.Info("incoming secret created")
		return &secret, true, nil
	}
	return &secret, false, nil
}

// TODO remove newModel handling after only new model is used
func (r *ComponentBuildReconciler) lookupPaCSecret(ctx context.Context, component *compapiv1alpha1.Component, gitProvider string, newModel bool) (*corev1.Secret, error) {
	log := ctrllog.FromContext(ctx)

	repoUrl := getGitRepoUrl(*component, newModel)
	var scmComponent *git.ScmComponent
	var err error
	if newModel {
		scmComponent, err = git.NewScmComponent(gitProvider, repoUrl, "", component.Name, component.Namespace)
	} else {
		scmComponent, err = git.NewScmComponent(gitProvider, repoUrl, component.Spec.Source.GitSource.Revision, component.Name, component.Namespace)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create SCM component: %w", err)
	}
	// find the best matching secret, starting from SSH type
	secret, err := r.CredentialProvider.LookupSecret(ctx, scmComponent, corev1.SecretTypeSSHAuth)
	if err != nil && !boerrors.IsBuildOpError(err, boerrors.EComponentGitSecretMissing) {
		log.Error(err, "failed to get Pipelines as Code SSH secret", "scmComponent", scmComponent)
		return nil, fmt.Errorf("failed to lookup SSH secret: %w", err)
	}
	if secret != nil {
		return secret, nil
	}
	// find the best matching secret, starting from BasicAuth type
	secret, err = r.CredentialProvider.LookupSecret(ctx, scmComponent, corev1.SecretTypeBasicAuth)
	if err != nil && !boerrors.IsBuildOpError(err, boerrors.EComponentGitSecretMissing) {
		log.Error(err, "failed to get Pipelines as Code BasicAuth secret", "scmComponent", scmComponent)
		return nil, fmt.Errorf("failed to lookup BasicAuth secret: %w", err)
	}
	if secret != nil {
		return secret, nil
	}

	// No SCM secrets found in the component namespace, fall back to the global configuration
	if gitProvider == "github" {
		return r.lookupGHAppSecret(ctx)
	} else {
		return nil, boerrors.NewBuildOpError(boerrors.EPaCSecretNotFound, fmt.Errorf("no matching Pipelines as Code secrets found in %s namespace", component.Namespace))
	}
}

func (r *ComponentBuildReconciler) lookupGHAppSecret(ctx context.Context) (*corev1.Secret, error) {
	pacSecret := &corev1.Secret{}
	globalPaCSecretKey := types.NamespacedName{Namespace: common.BuildServiceNamespaceName, Name: common.PipelinesAsCodeGitHubAppSecretName}
	if err := r.Client.Get(ctx, globalPaCSecretKey, pacSecret); err != nil {
		if !errors.IsNotFound(err) {
			r.EventRecorder.Event(pacSecret, "Warning", "ErrorReadingPaCSecret", err.Error())
			return nil, fmt.Errorf("failed to get Pipelines as Code secret in %s namespace: %w", globalPaCSecretKey.Namespace, err)
		}

		r.EventRecorder.Event(pacSecret, "Warning", "PaCSecretNotFound", err.Error())
		// Do not trigger a new reconcile. The PaC secret must be created first.
		return nil, boerrors.NewBuildOpError(boerrors.EPaCSecretNotFound, fmt.Errorf(" Pipelines as Code secret not found in %s ", globalPaCSecretKey.Namespace))
	}
	return pacSecret, nil
}

// TODO remove newModel handling after only new model is used
// Returns webhook secret for given component.
// Generates the webhook secret and saves it in the k8s secret if it doesn't exist.
func (r *ComponentBuildReconciler) ensureWebhookSecret(ctx context.Context, component *compapiv1alpha1.Component, newModel bool) (string, error) {
	log := ctrllog.FromContext(ctx)

	webhookSecretsSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: pipelinesAsCodeWebhooksSecretName, Namespace: component.Namespace}, webhookSecretsSecret); err != nil {
		if errors.IsNotFound(err) {
			webhookSecretsSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pipelinesAsCodeWebhooksSecretName,
					Namespace: component.Namespace,
					Labels: map[string]string{
						PartOfLabelName: PartOfAppStudioLabelValue,
					},
				},
			}
			if err := r.Client.Create(ctx, webhookSecretsSecret); err != nil {
				log.Error(err, "failed to create webhooks secrets secret", l.Action, l.ActionAdd)
				return "", fmt.Errorf("failed to create webhook secrets secret: %w", err)
			}
			return r.ensureWebhookSecret(ctx, component, newModel)
		}

		log.Error(err, "failed to get webhook secrets secret", l.Action, l.ActionView)
		return "", fmt.Errorf("failed to get webhook secrets secret: %w", err)
	}

	componentWebhookSecretKey := getWebhookSecretKeyForComponent(*component, newModel)
	if _, exists := webhookSecretsSecret.Data[componentWebhookSecretKey]; exists {
		// The webhook secret already exists. Use single secret for the same repository.
		return string(webhookSecretsSecret.Data[componentWebhookSecretKey]), nil
	}

	webhookSecretString := generatePaCWebhookSecretString()

	if webhookSecretsSecret.Data == nil {
		webhookSecretsSecret.Data = make(map[string][]byte)
	}
	webhookSecretsSecret.Data[componentWebhookSecretKey] = []byte(webhookSecretString)
	if err := r.Client.Update(ctx, webhookSecretsSecret); err != nil {
		log.Error(err, "failed to update webhook secrets secret", l.Action, l.ActionUpdate)
		return "", fmt.Errorf("failed to update webhook secrets secret: %w", err)
	}

	return webhookSecretString, nil
}

// TODO remove newModel handling after only new model is used
func getWebhookSecretKeyForComponent(component compapiv1alpha1.Component, newModel bool) string {
	gitRepoUrl := getGitRepoUrl(component, newModel)

	notAllowedCharRegex, _ := regexp.Compile("[^-._a-zA-Z0-9]{1}")
	return notAllowedCharRegex.ReplaceAllString(gitRepoUrl, "_")
}

// generatePaCWebhookSecretString generates string alike openssl rand -hex 20
func generatePaCWebhookSecretString() string {
	length := 20 // in bytes
	tokenBytes := make([]byte, length)
	if _, err := rand.Read(tokenBytes); err != nil {
		panic("Failed to read from random generator")
	}
	return hex.EncodeToString(tokenBytes)
}

// getPaCIncomingSecretName returns the name of the incoming secret for a PaC repository
func getPaCIncomingSecretName(repositoryName string) string {
	return fmt.Sprintf("%s%s", repositoryName, pacIncomingSecretNameSuffix)
}
