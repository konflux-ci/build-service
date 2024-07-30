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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	. "github.com/konflux-ci/build-service/pkg/common"
	"github.com/konflux-ci/build-service/pkg/git"
	l "github.com/konflux-ci/build-service/pkg/logs"
)

// ensureIncomingSecret is ensuring that incoming secret for PaC trigger exists
// if secret doesn't exists it will create it and also add repository as owner
// Returns:
// pointer to secret object
// bool which indicates if reconcile is required (which is required when we just created secret)
func (r *ComponentBuildReconciler) ensureIncomingSecret(ctx context.Context, component *appstudiov1alpha1.Component) (*corev1.Secret, bool, error) {
	log := ctrllog.FromContext(ctx)

	repository, err := r.findPaCRepositoryForComponent(ctx, component)
	if err != nil {
		return nil, false, err
	}

	incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)
	incomingSecretPassword := generatePaCWebhookSecretString()
	incomingSecretData := map[string]string{
		pacIncomingSecretKey: incomingSecretPassword,
	}

	secret := corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}, &secret); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "failed to get incoming secret", l.Action, l.ActionView)
			return nil, false, err
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
			return nil, false, err
		}

		if err := r.Client.Create(ctx, &secret); err != nil {
			log.Error(err, "failed to create incoming secret", l.Action, l.ActionAdd)
			return nil, false, err
		}

		log.Info("incoming secret created")
		return &secret, true, nil
	}
	return &secret, false, nil
}

func (r *ComponentBuildReconciler) lookupPaCSecret(ctx context.Context, component *appstudiov1alpha1.Component, gitProvider string) (*corev1.Secret, error) {
	log := ctrllog.FromContext(ctx)

	scmComponent, err := git.NewScmComponent(gitProvider, component.Spec.Source.GitSource.URL, component.Spec.Source.GitSource.Revision, component.Name, component.Namespace)
	if err != nil {
		return nil, err
	}
	// find the best matching secret, starting from SSH type
	secret, err := r.CredentialProvider.LookupSecret(ctx, scmComponent, corev1.SecretTypeSSHAuth)
	if err != nil && !boerrors.IsBuildOpError(err, boerrors.EComponentGitSecretMissing) {
		log.Error(err, "failed to get Pipelines as Code SSH secret", "scmComponent", scmComponent)
		return nil, err
	}
	if secret != nil {
		return secret, nil
	}
	// find the best matching secret, starting from BasicAuth type
	secret, err = r.CredentialProvider.LookupSecret(ctx, scmComponent, corev1.SecretTypeBasicAuth)
	if err != nil && !boerrors.IsBuildOpError(err, boerrors.EComponentGitSecretMissing) {
		log.Error(err, "failed to get Pipelines as Code BasicAuth secret", "scmComponent", scmComponent)
		return nil, err
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
	globalPaCSecretKey := types.NamespacedName{Namespace: BuildServiceNamespaceName, Name: PipelinesAsCodeGitHubAppSecretName}
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

// Returns webhook secret for given component.
// Generates the webhook secret and saves it in the k8s secret if it doesn't exist.
func (r *ComponentBuildReconciler) ensureWebhookSecret(ctx context.Context, component *appstudiov1alpha1.Component) (string, error) {
	log := ctrllog.FromContext(ctx)

	webhookSecretsSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: pipelinesAsCodeWebhooksSecretName, Namespace: component.GetNamespace()}, webhookSecretsSecret); err != nil {
		if errors.IsNotFound(err) {
			webhookSecretsSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pipelinesAsCodeWebhooksSecretName,
					Namespace: component.GetNamespace(),
					Labels: map[string]string{
						PartOfLabelName: PartOfAppStudioLabelValue,
					},
				},
			}
			if err := r.Client.Create(ctx, webhookSecretsSecret); err != nil {
				log.Error(err, "failed to create webhooks secrets secret", l.Action, l.ActionAdd)
				return "", err
			}
			return r.ensureWebhookSecret(ctx, component)
		}

		log.Error(err, "failed to get webhook secrets secret", l.Action, l.ActionView)
		return "", err
	}

	componentWebhookSecretKey := getWebhookSecretKeyForComponent(*component)
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
		return "", err
	}

	return webhookSecretString, nil
}

func getWebhookSecretKeyForComponent(component appstudiov1alpha1.Component) string {
	gitRepoUrl := strings.TrimSuffix(component.Spec.Source.GitSource.URL, ".git")

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
