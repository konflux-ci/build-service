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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/pkg/devfile"
	"github.com/redhat-appstudio/build-service/pkg/boerrors"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	buildPipelineServiceAccountName = "pipeline"
)

// SubmitNewBuild creates a new PipelineRun to build a new image for the given component.
// Is called only once on component creation if Pipelines as Code is not configured,
// otherwise the build is handled by PaC.
func (r *ComponentBuildReconciler) SubmitNewBuild(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := r.Log.WithValues("Namespace", component.Namespace, "Application", component.Spec.Application, "Component", component.Name)

	// Create pipeline service account
	pipelinesServiceAccount := corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: "pipeline", Namespace: component.Namespace}, &pipelinesServiceAccount)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("Failed to read service account %s in namespace %s", buildPipelineServiceAccountName, component.Namespace))
			return err
		}
		// Create service account for the build pipeline
		buildPipelineSA := corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      buildPipelineServiceAccountName,
				Namespace: component.Namespace,
			},
		}
		if err := r.Client.Create(ctx, &buildPipelineSA); err != nil {
			log.Error(err, fmt.Sprintf("Failed to create service account %s in namespace %s", buildPipelineServiceAccountName, component.Namespace))
			return err
		}
		return r.SubmitNewBuild(ctx, component)
	}

	// Link git secret to pipeline service account if needed
	gitSecretName := component.Spec.Secret
	if gitSecretName != "" {
		gitSecret := corev1.Secret{}
		err := r.Client.Get(ctx, types.NamespacedName{Name: gitSecretName, Namespace: component.Namespace}, &gitSecret)
		if err != nil {
			log.Error(err, fmt.Sprintf("Secret %s is missing", gitSecretName))
			return boerrors.NewBuildOpError(boerrors.EComponentGitSecretMissing, err)
		}

		// Make the secret ready for consumption by Tekton
		if gitSecret.Annotations == nil {
			gitSecret.Annotations = map[string]string{}
		}
		gitHost, _ := getGitProviderUrl(component.Spec.Source.GitSource.URL)
		// Doesn't matter if it was present, we will always override because we clone from one repository only
		gitSecret.Annotations["tekton.dev/git-0"] = gitHost
		if err = r.Client.Update(ctx, &gitSecret); err != nil {
			log.Error(err, fmt.Sprintf("Secret %s update failed", gitSecretName))
			return err
		}

		updateRequired := updateServiceAccountIfSecretNotLinked(gitSecretName, &pipelinesServiceAccount)
		if updateRequired {
			if err := r.Client.Update(ctx, &pipelinesServiceAccount); err != nil {
				log.Error(err, fmt.Sprintf("Unable to update pipeline service account %v", pipelinesServiceAccount))
				return err
			}
			log.Info(fmt.Sprintf("Service Account %v updated with git secret", pipelinesServiceAccount))
		}
	}

	// Link image registry secret to pipeline service account if needed
	if _, exists := component.Annotations[ImageRepoGenerateAnnotationName]; exists {
		imageRepo, imageRepoSecretName, err := getComponentImageRepoAndSecretNameFromImageAnnotation(component)
		if err != nil {
			return err
		}
		if imageRepo != "" && imageRepoSecretName != "" {
			// Annotate the image repo secret to make it respected by Tekton
			dockerSecret := corev1.Secret{}
			err := r.Client.Get(ctx, types.NamespacedName{Name: imageRepoSecretName, Namespace: component.Namespace}, &dockerSecret)
			if err != nil {
				log.Error(err, fmt.Sprintf("Secret %s is missing", imageRepoSecretName))
				return boerrors.NewBuildOpError(boerrors.EComponentDockerSecretMissing, err)
			}
			if dockerSecret.Annotations == nil {
				dockerSecret.Annotations = map[string]string{}
			}
			// We can always use 0 index because the component uses only one image repository
			dockerSecret.Annotations["tekton.dev/docker-0"] = "https://quay.io"

			updateRequired := updateServiceAccountIfSecretNotLinked(dockerSecret.Name, &pipelinesServiceAccount)
			if updateRequired {
				err = r.Client.Update(ctx, &pipelinesServiceAccount)
				if err != nil {
					log.Error(err, fmt.Sprintf("Unable to update pipeline service account %v", pipelinesServiceAccount))
					return err
				}
				log.Info(fmt.Sprintf("Service Account %v updated with image secret", pipelinesServiceAccount))
			}

			// Add finalizer to clean up docker secret link on component deletion
			if err := r.Client.Get(ctx, types.NamespacedName{Namespace: component.Namespace, Name: component.Name}, component); err != nil {
				log.Error(err, "failed to get Component")
				return err
			}
			if component.ObjectMeta.DeletionTimestamp.IsZero() {
				if !controllerutil.ContainsFinalizer(component, ImageRegistrySecretLinkFinalizer) {
					controllerutil.AddFinalizer(component, ImageRegistrySecretLinkFinalizer)
					log.Info("Image registry secret service account link finalizer added")
				}
			}
			if err := r.Client.Update(ctx, component); err != nil {
				return err
			}
		}
	}

	// Create initial build pipeline

	pipelineRef, additionalPipelineParams, err := r.GetPipelineForComponent(ctx, component)
	if err != nil {
		return err
	}

	initialBuildPipelineRun, err := generateInitialPipelineRunForComponent(component, pipelineRef, additionalPipelineParams)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to generate PipelineRun to build %s component in %s namespace", component.Name, component.Namespace))
		return err
	}

	err = controllerutil.SetOwnerReference(component, initialBuildPipelineRun, r.Scheme)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to set owner reference for %v", initialBuildPipelineRun))
	}

	err = r.Client.Create(ctx, initialBuildPipelineRun)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to create the build PipelineRun %v", initialBuildPipelineRun))
		return err
	}

	initialBuildPipelineCreationTimeMetric.Observe(time.Since(component.CreationTimestamp.Time).Seconds())

	log.Info(fmt.Sprintf("Initial build pipeline %s created for component %s in %s namespace using %s pipeline from %s bundle",
		initialBuildPipelineRun.Name, component.Name, component.Namespace, pipelineRef.Name, pipelineRef.Bundle))

	return nil
}

func generateInitialPipelineRunForComponent(component *appstudiov1alpha1.Component, pipelineRef *tektonapi.PipelineRef, additionalPipelineParams []tektonapi.Param) (*tektonapi.PipelineRun, error) {
	timestamp := time.Now().Unix()
	pipelineGenerateName := fmt.Sprintf("%s-", component.Name)
	revision := ""
	if component.Spec.Source.GitSource != nil && component.Spec.Source.GitSource.Revision != "" {
		revision = component.Spec.Source.GitSource.Revision
	}

	imageRepo, err := getImageRepositoryForComponent(component)
	if err != nil {
		return nil, err
	}
	image := fmt.Sprintf("%s:build-%s-%d", imageRepo, getRandomString(5), timestamp)

	params := []tektonapi.Param{
		{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: component.Spec.Source.GitSource.URL}},
		{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: revision}},
		{Name: "output-image", Value: tektonapi.ArrayOrString{Type: "string", StringVal: image}},
	}
	if value, exists := component.Annotations["skip-initial-checks"]; exists && (value == "1" || strings.ToLower(value) == "true") {
		params = append(params, tektonapi.Param{Name: "skip-checks", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}})
	}

	dockerFile, err := devfile.SearchForDockerfile([]byte(component.Status.Devfile))
	if err != nil {
		return nil, err
	}
	if dockerFile != nil {
		if dockerFile.Uri != "" {
			params = append(params, tektonapi.Param{Name: "dockerfile", Value: tektonapi.ArrayOrString{Type: "string", StringVal: dockerFile.Uri}})
		}
		pathContext := getPathContext(component.Spec.Source.GitSource.Context, dockerFile.BuildContext)
		if pathContext != "" {
			params = append(params, tektonapi.Param{Name: "path-context", Value: tektonapi.ArrayOrString{Type: "string", StringVal: pathContext}})
		}
	}

	params = mergeAndSortTektonParams(params, additionalPipelineParams)

	pipelineRun := &tektonapi.PipelineRun{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PipelineRun",
			APIVersion: "tekton.dev/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: pipelineGenerateName,
			Namespace:    component.Namespace,
			Labels: map[string]string{
				ApplicationNameLabelName:                component.Spec.Application,
				ComponentNameLabelName:                  component.Name,
				"pipelines.appstudio.openshift.io/type": "build",
			},
			Annotations: map[string]string{
				"build.appstudio.redhat.com/target_branch": revision,
				"build.appstudio.redhat.com/pipeline_name": pipelineRef.Name,
				"build.appstudio.redhat.com/bundle":        pipelineRef.Bundle,
			},
		},
		Spec: tektonapi.PipelineRunSpec{
			PipelineRef: pipelineRef,
			Params:      params,
			Workspaces: []tektonapi.WorkspaceBinding{
				{
					Name:                "workspace",
					VolumeClaimTemplate: generateVolumeClaimTemplate(),
				},
			},
		},
	}

	return pipelineRun, nil
}

// getGitProviderUrl takes a Git URL and returns git provider host.
// Examples:
//
//	For https://github.com/foo/bar returns https://github.com
//	For git@github.com:foo/bar returns https://github.com
func getGitProviderUrl(gitURL string) (string, error) {
	if strings.HasPrefix(gitURL, "git@") {
		host := strings.Split(strings.TrimPrefix(gitURL, "git@"), ":")[0]
		return "https://" + host, nil
	}

	u, err := url.Parse(gitURL)

	// We really need the format of the string to be correct.
	// We'll not do any autocorrection.
	if err != nil || u.Scheme == "" {
		return "", fmt.Errorf("failed to parse string into a URL: %v or scheme is empty", err)
	}
	return u.Scheme + "://" + u.Host, nil
}

func updateServiceAccountIfSecretNotLinked(secretName string, serviceAccount *corev1.ServiceAccount) bool {
	if secretName == "" {
		// The secret is empty, no updates needed
		return false
	}
	for _, credentialSecret := range serviceAccount.Secrets {
		if credentialSecret.Name == secretName {
			// The secret is present in the service account, no updates needed
			return false
		}
	}

	// Add the secret to the service account and return that update is needed
	serviceAccount.Secrets = append(serviceAccount.Secrets, corev1.ObjectReference{Name: secretName})
	return true
}

func getRandomString(length int) string {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		panic("Failed to read from random generator")
	}
	return hex.EncodeToString(bytes)[0:length]
}
