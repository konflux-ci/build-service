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
	"regexp"
	"strings"
	"time"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	"github.com/redhat-appstudio/application-service/pkg/devfile"
	"github.com/redhat-appstudio/build-service/pkg/boerrors"
	"github.com/redhat-appstudio/build-service/pkg/git/gitproviderfactory"
	l "github.com/redhat-appstudio/build-service/pkg/logs"
	appstudiospiapiv1beta1 "github.com/redhat-appstudio/service-provider-integration-operator/api/v1beta1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// SubmitNewBuild creates a new PipelineRun to build a new image for the given component.
// Is called right ater component creation and later on user's demand.
func (r *ComponentBuildReconciler) SubmitNewBuild(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := ctrllog.FromContext(ctx).WithName("SimpleBuild")
	ctx = ctrllog.IntoContext(ctx, log)

	pipelineRef, additionalPipelineParams, err := r.GetPipelineForComponent(ctx, component)
	if err != nil {
		return err
	}

	pacSecret := corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: buildServiceNamespaceName, Name: gitopsprepare.PipelinesAsCodeSecretName}, &pacSecret); err != nil {
		log.Error(err, "failed to get git provider credentials secret", l.Action, l.ActionView)
		return err
	}
	buildGitInfo, err := r.getBuildGitInfo(ctx, component, pacSecret.Data)
	if err != nil {
		return err
	}

	buildPipelineRun, err := generatePipelineRunForComponent(component, pipelineRef, additionalPipelineParams, buildGitInfo)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to generate PipelineRun to build %s component in %s namespace", component.Name, component.Namespace))
		return err
	}

	err = controllerutil.SetOwnerReference(component, buildPipelineRun, r.Scheme)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to set owner reference for %v", buildPipelineRun), l.Action, l.ActionUpdate)
	}

	err = r.Client.Create(ctx, buildPipelineRun)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to create the build PipelineRun %v", buildPipelineRun), l.Action, l.ActionAdd)
		return err
	}

	simpleBuildPipelineCreationTimeMetric.Observe(time.Since(component.CreationTimestamp.Time).Seconds())

	log.Info(fmt.Sprintf("Build pipeline %s created for component %s in %s namespace using %s pipeline from %s bundle",
		buildPipelineRun.Name, component.Name, component.Namespace, pipelineRef.Name, pipelineRef.Bundle),
		l.Action, l.ActionAdd, l.Audit, "true")

	return nil
}

type buildGitInfo struct {
	// isPublic shows if component git repository publicly accessible.
	isPublic bool
	// gitSecretName contains name of the k8s secret with credentials to access component private git repository.
	gitSecretName string

	// These fields are optional for the build and are shown on UI only.
	gitSourceSha              string
	browseRepositoryAtShaLink string
}

// getBuildGitInfo find out git source information the build is done from.
func (r *ComponentBuildReconciler) getBuildGitInfo(ctx context.Context, component *appstudiov1alpha1.Component, pacConfig map[string][]byte) (*buildGitInfo, error) {
	log := ctrllog.FromContext(ctx).WithName("getBuildGitInfo")

	gitProvider, err := gitops.GetGitProvider(*component)
	if err != nil {
		// There is no point to continue if git provider is not known
		return nil, fmt.Errorf("error detecting git provider: %w", err)
	}

	repoUrl := component.Spec.Source.GitSource.URL

	gitClient, err := gitproviderfactory.CreateGitClient(gitproviderfactory.GitClientConfig{
		PacSecretData:             pacConfig,
		GitProvider:               gitProvider,
		RepoUrl:                   repoUrl,
		IsAppInstallationExpected: false,
	})
	if err != nil {
		log.Error(err, "failed to instantiate git client")
		return nil, err
	}

	var gitSecretName string
	isPublic, err := gitClient.IsRepositoryPublic(repoUrl)
	if err != nil {
		log.Error(err, "failed to determine whether component git repository public or private")
		return nil, err
	}
	if !isPublic {
		// Try to find git secret in case private git repository is used
		gitSecretName, err = r.findGitSecretName(ctx, component)
		if err != nil {
			log.Error(err, "failed to find git secret name")
			return nil, err
		}
		if gitSecretName == "" {
			// The repository is private and no git secret provided
			return nil, boerrors.NewBuildOpError(boerrors.EComponentGitSecretMissing, nil)
		}
		log.Info("Git secret found", "GitSecretName", gitSecretName)
	}

	// Find out git source commit SHA to build from and repositopry link.
	// This is optional for the build itself, but needed for UI to correctly display the build pipeline.
	// Skip any errors occured during git information fetching.
	gitSourceSha := ""
	revision := component.Spec.Source.GitSource.Revision
	if revision != "" {
		// Check if commit sha is given in the revision
		matches, err := regexp.MatchString("[0-9a-fA-F]{7,40}", revision)
		if err != nil {
			panic("invalid regexp")
		}
		if matches {
			gitSourceSha = revision
		}
	}
	if gitSourceSha == "" {
		gitSourceSha, err = gitClient.GetBranchSha(repoUrl, revision)
		if err != nil {
			log.Error(err, "failed to get git branch SHA, continue without it")
		}
	}

	var browseRepositoryAtShaLink string
	if gitSourceSha != "" {
		browseRepositoryAtShaLink = gitClient.GetBrowseRepositoryAtShaLink(repoUrl, gitSourceSha)
	}

	return &buildGitInfo{
		isPublic:      isPublic,
		gitSecretName: gitSecretName,

		gitSourceSha:              gitSourceSha,
		browseRepositoryAtShaLink: browseRepositoryAtShaLink,
	}, nil
}

func (r *ComponentBuildReconciler) findGitSecretName(ctx context.Context, component *appstudiov1alpha1.Component) (string, error) {
	log := ctrllog.FromContext(ctx).WithName("getGitSecretName")

	spiAccessTokensList := &appstudiospiapiv1beta1.SPIAccessTokenBindingList{}
	if err := r.Client.List(ctx, spiAccessTokensList, &client.ListOptions{Namespace: component.Namespace}); err != nil {
		log.Error(err, "failed to list SPIAccessTokenBindings")
		return "", err
	}

	for _, atb := range spiAccessTokensList.Items {
		if atb.Spec.RepoUrl == component.Spec.Source.GitSource.URL {
			return atb.Status.SyncedObjectRef.Name, nil
		}
	}
	// Needed binding not found
	return "", nil
}

func generatePipelineRunForComponent(component *appstudiov1alpha1.Component, pipelineRef *tektonapi.PipelineRef, additionalPipelineParams []tektonapi.Param, pRunGitInfo *buildGitInfo) (*tektonapi.PipelineRun, error) {
	timestamp := time.Now().Unix()
	pipelineGenerateName := fmt.Sprintf("%s-", component.Name)
	revision := ""
	if component.Spec.Source.GitSource != nil && component.Spec.Source.GitSource.Revision != "" {
		revision = component.Spec.Source.GitSource.Revision
	}

	annotations := map[string]string{
		"build.appstudio.redhat.com/pipeline_name": pipelineRef.Name,
		"build.appstudio.redhat.com/bundle":        pipelineRef.Bundle,
	}
	if revision != "" {
		annotations[gitTargetBranchAnnotationName] = revision
	}

	imageRepo := getContainerImageRepositoryForComponent(component)
	image := fmt.Sprintf("%s:build-%s-%d", imageRepo, getRandomString(5), timestamp)

	params := []tektonapi.Param{
		{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: component.Spec.Source.GitSource.URL}},
		{Name: "output-image", Value: tektonapi.ArrayOrString{Type: "string", StringVal: image}},
	}
	if revision != "" {
		params = append(params, tektonapi.Param{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: revision}})
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
			Annotations: annotations,
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

	// Add git source info to the pipeline run
	if pRunGitInfo != nil {
		if pRunGitInfo.gitSourceSha != "" {
			pipelineRun.Annotations[gitCommitShaAnnotationName] = pRunGitInfo.gitSourceSha
		}
		if pRunGitInfo.browseRepositoryAtShaLink != "" {
			pipelineRun.Annotations[gitRepoAtShaAnnotationName] = pRunGitInfo.browseRepositoryAtShaLink
		}
		if pRunGitInfo.gitSecretName != "" {
			pipelineRun.Spec.Workspaces = append(pipelineRun.Spec.Workspaces, tektonapi.WorkspaceBinding{
				Name:   "git-auth",
				Secret: &corev1.SecretVolumeSource{SecretName: pRunGitInfo.gitSecretName},
			})
		}
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

func getRandomString(length int) string {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		panic("Failed to read from random generator")
	}
	return hex.EncodeToString(bytes)[0:length]
}
