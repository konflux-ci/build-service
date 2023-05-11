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
	"strconv"
	"strings"
	"time"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	"github.com/redhat-appstudio/application-service/pkg/devfile"
	"github.com/redhat-appstudio/build-service/pkg/boerrors"
	"github.com/redhat-appstudio/build-service/pkg/github"
	"github.com/redhat-appstudio/build-service/pkg/gitlab"
	l "github.com/redhat-appstudio/build-service/pkg/logs"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// SubmitNewBuild creates a new PipelineRun to build a new image for the given component.
// Is called only once on component creation if Pipelines as Code is not configured,
// otherwise the build is handled by PaC.
func (r *ComponentBuildReconciler) SubmitNewBuild(ctx context.Context, component *appstudiov1alpha1.Component) error {
	log := ctrllog.FromContext(ctx).WithName("SimpleBuild")
	ctx = ctrllog.IntoContext(ctx, log)

	// Link git secret to pipeline service account if needed
	gitSecretName := component.Spec.Secret
	if gitSecretName != "" {
		gitSecret := corev1.Secret{}
		err := r.Client.Get(ctx, types.NamespacedName{Name: gitSecretName, Namespace: component.Namespace}, &gitSecret)
		if err != nil {
			if errors.IsNotFound(err) {
				log.Error(err, fmt.Sprintf("Secret %s is missing", gitSecretName), l.Action, l.ActionView)
				return boerrors.NewBuildOpError(boerrors.EComponentGitSecretMissing, err)
			}
			return err
		}

		// Make the secret ready for consumption by Tekton
		if gitSecret.Annotations == nil {
			gitSecret.Annotations = map[string]string{}
		}
		gitHost, _ := getGitProviderUrl(component.Spec.Source.GitSource.URL)
		// Doesn't matter if the annotation was present, we will always override because we clone from one repository only
		if gitSecret.Annotations["tekton.dev/git-0"] != gitHost {
			gitSecret.Annotations["tekton.dev/git-0"] = gitHost
			if err = r.Client.Update(ctx, &gitSecret); err != nil {
				log.Error(err, fmt.Sprintf("Secret %s update failed", gitSecretName), l.Action, l.ActionUpdate)
				return err
			}
		}

		_, err = r.linkSecretToServiceAccount(ctx, gitSecretName, buildPipelineServiceAccountName, component.Namespace, false)
		if err != nil {
			return err
		}
		// link secret also to old pipeline account, can be removed when default pipeline is switched to appstudio-pipeline
		_, _ = r.linkSecretToServiceAccount(ctx, gitSecretName, "pipeline", component.Namespace, false)
	}

	// Create initial build pipeline

	pipelineRef, additionalPipelineParams, err := r.GetPipelineForComponent(ctx, component)
	if err != nil {
		return err
	}

	// Find out source commit SHA to build from.
	// This is optional for the build itself, but needed for UI to correctly display build pipeline.
	// Skip any errors occured during SHA fetching.
	gitSourceSHA := ""
	revision := component.Spec.Source.GitSource.Revision
	if revision != "" {
		// Check if commit sha is given in the revision
		matches, err := regexp.MatchString("[0-9a-fA-F]{40}", revision)
		if err != nil {
			return err
		}
		if matches {
			gitSourceSHA = revision
		}
	}
	if gitSourceSHA == "" {
		pacSecret := corev1.Secret{}
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: buildServiceNamespaceName, Name: gitopsprepare.PipelinesAsCodeSecretName}, &pacSecret); err == nil {
			gitSourceSHA, err = getGitSourceShaForComponent(component, pacSecret.Data)
			if err != nil {
				log.Error(err, "Failed to retrieve git source commit SHA", l.Action, l.ActionView, l.Audit, "true")
			}
		} else {
			log.Error(err, "error getting git provider credentials secret", l.Action, l.ActionView)
		}
	}

	initialBuildPipelineRun, err := generateInitialPipelineRunForComponent(component, pipelineRef, additionalPipelineParams, gitSourceSHA)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to generate PipelineRun to build %s component in %s namespace", component.Name, component.Namespace))
		return err
	}

	err = controllerutil.SetOwnerReference(component, initialBuildPipelineRun, r.Scheme)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to set owner reference for %v", initialBuildPipelineRun), l.Action, l.ActionUpdate)
	}

	err = r.Client.Create(ctx, initialBuildPipelineRun)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to create the build PipelineRun %v", initialBuildPipelineRun), l.Action, l.ActionAdd)
		return err
	}

	initialBuildPipelineCreationTimeMetric.Observe(time.Since(component.CreationTimestamp.Time).Seconds())

	log.Info(fmt.Sprintf("Build pipeline %s created for component %s in %s namespace using %s pipeline from %s bundle",
		initialBuildPipelineRun.Name, component.Name, component.Namespace, pipelineRef.Name, pipelineRef.Bundle),
		l.Action, l.ActionAdd, l.Audit, "true")

	return nil
}

func getGitSourceShaForComponent(component *appstudiov1alpha1.Component, pacConfig map[string][]byte) (string, error) {
	gitProvider, err := gitops.GetGitProvider(*component)
	if err != nil {
		// There is no point to continue if git provider is not known
		return "", fmt.Errorf("error detecting git provider: %w", err)
	}

	isAppUsed := gitops.IsPaCApplicationConfigured(gitProvider, pacConfig)

	var accessToken string
	if !isAppUsed {
		accessToken = strings.TrimSpace(string(pacConfig[gitops.GetProviderTokenKey(gitProvider)]))
	}

	// https://github.com/owner/repository
	gitSourceUrlParts := strings.Split(strings.TrimSuffix(component.Spec.Source.GitSource.URL, ".git"), "/")

	switch gitProvider {
	case "github":
		owner := gitSourceUrlParts[3]
		repository := gitSourceUrlParts[4]

		var ghclient *github.GithubClient
		if isAppUsed {
			githubAppIdStr := string(pacConfig[gitops.PipelinesAsCode_githubAppIdKey])
			githubAppId, err := strconv.ParseInt(githubAppIdStr, 10, 64)
			if err != nil {
				return "", fmt.Errorf("failed to convert %s to int: %w", githubAppIdStr, err)
			}

			privateKey := pacConfig[gitops.PipelinesAsCode_githubPrivateKey]
			ghclient, err = github.NewGithubClientForSimpleBuildByApp(githubAppId, privateKey)
			if err != nil {
				return "", fmt.Errorf("failed to create GitHub client for simple build: %w", err)
			}
		} else {
			ghclient = github.NewGithubClient(accessToken)
		}

		branchName := component.Spec.Source.GitSource.Revision
		if branchName == "" {
			branchName, err = github.GetDefaultBranch(ghclient, owner, repository)
			if err != nil {
				return "", nil
			}
		}

		return github.GetBranchSHA(ghclient, owner, repository, branchName)

	case "gitlab":
		glclient, err := gitlab.NewGitlabClient(accessToken)
		if err != nil {
			return "", err
		}

		gitlabNamespace := gitSourceUrlParts[3]
		gitlabProjectName := gitSourceUrlParts[4]
		projectPath := gitlabNamespace + "/" + gitlabProjectName

		branchName := component.Spec.Source.GitSource.Revision
		if branchName == "" {
			branchName, err = gitlab.GetDefaultBranch(glclient, projectPath)
			if err != nil {
				return "", nil
			}
		}

		return gitlab.GetBranchSHA(glclient, projectPath, branchName)

	case "bitbucket":
		// TODO implement
		return "", fmt.Errorf("git provider %s is not supported", gitProvider)
	default:
		return "", fmt.Errorf("git provider %s is not supported", gitProvider)
	}
}

func generateInitialPipelineRunForComponent(component *appstudiov1alpha1.Component, pipelineRef *tektonapi.PipelineRef, additionalPipelineParams []tektonapi.Param, gitSourceSHA string) (*tektonapi.PipelineRun, error) {
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

	if gitSourceSHA != "" {
		pipelineRun.Annotations[gitCommitShaAnnotationName] = gitSourceSHA

		gitProvider, _ := gitops.GetGitProvider(*component)
		switch gitProvider {
		case "github":
			pipelineRun.Annotations[gitRepoAtShaAnnotationName] = github.GetBrowseRepositoryAtShaLink(component.Spec.Source.GitSource.URL, gitSourceSHA)
		case "gitlab":
			pipelineRun.Annotations[gitRepoAtShaAnnotationName] = gitlab.GetBrowseRepositoryAtShaLink(component.Spec.Source.GitSource.URL, gitSourceSHA)
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
