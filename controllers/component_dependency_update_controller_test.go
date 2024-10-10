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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gitgithub "github.com/google/go-github/v45/github"
	applicationapi "github.com/konflux-ci/application-api/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	. "github.com/konflux-ci/build-service/pkg/common"
	"github.com/konflux-ci/build-service/pkg/git/github"
	//+kubebuilder:scaffold:imports
)

const (
	UserNamespace = "user1-tenant"
	BaseComponent = "base-component"
	Operator1     = "operator1"
	Operator2     = "operator2"
	ImageUrl      = "IMAGE_URL"
	ImageDigest   = "IMAGE_DIGEST"
)

var failures = 0

var _ = Describe("Component nudge controller", func() {

	delayTime = 0
	BeforeEach(func() {
		createNamespace(UserNamespace)
		baseComponentName := types.NamespacedName{Namespace: UserNamespace, Name: BaseComponent}
		createCustomComponentWithoutBuildRequest(componentConfig{
			componentKey:   baseComponentName,
			containerImage: "quay.io/organization/repo:tag",
		})
		createComponent(types.NamespacedName{Namespace: UserNamespace, Name: Operator1})
		createComponent(types.NamespacedName{Namespace: UserNamespace, Name: Operator2})
		baseComponent := applicationapi.Component{}
		err := k8sClient.Get(context.TODO(), baseComponentName, &baseComponent)
		Expect(err).ToNot(HaveOccurred())
		baseComponent.Spec.BuildNudgesRef = []string{Operator1, Operator2}
		err = k8sClient.Update(context.TODO(), &baseComponent)
		Expect(err).ToNot(HaveOccurred())

		createNamespace(BuildServiceNamespaceName)
		pacSecretKey := types.NamespacedName{Name: PipelinesAsCodeGitHubAppSecretName, Namespace: BuildServiceNamespaceName}
		pacSecretData := map[string]string{
			"github-application-id": "12345",
			"github-private-key":    githubAppPrivateKey,
		}
		createSecret(pacSecretKey, pacSecretData)

		basicSecretKey := types.NamespacedName{Name: "basicauthsecret", Namespace: UserNamespace}
		basicSecretData := map[string]string{
			"password": "12345",
		}
		createSCMSecret(basicSecretKey, basicSecretData, v1.SecretTypeBasicAuth, map[string]string{})

		imageRepoSecretKey := types.NamespacedName{Name: "dockerconfigjsonsecret", Namespace: UserNamespace}
		dockerConfigJson := fmt.Sprintf(`{"auths":{"quay.io/organization/repo":{"auth":"%s"}}}`, base64.StdEncoding.EncodeToString([]byte("image_repo_username:image_repo_password")))
		imageRepoSecretData := map[string]string{
			".dockerconfigjson": dockerConfigJson,
		}
		createSCMSecret(imageRepoSecretKey, imageRepoSecretData, v1.SecretTypeDockerConfigJson, map[string]string{})

		serviceAccountKey := types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: UserNamespace}
		sa := waitServiceAccount(serviceAccountKey)
		sa.Secrets = []v1.ObjectReference{{Name: "dockerconfigjsonsecret"}}
		Expect(k8sClient.Update(ctx, &sa)).To(Succeed())

		github.GetAppInstallationsForRepository = func(githubAppIdStr string, appPrivateKeyPem []byte, repoUrl string) (*github.ApplicationInstallation, string, error) {
			repo_name := "repo_name"
			repo_fullname := "repo_fullname"
			repo_defaultbranch := "repo_defaultbranch"
			return &github.ApplicationInstallation{
				Token:        "some_token",
				ID:           1,
				Repositories: []*gitgithub.Repository{{Name: &repo_name, FullName: &repo_fullname, DefaultBranch: &repo_defaultbranch}},
			}, "slug", nil
		}
	})

	AfterEach(func() {
		failures = 0
		deleteListOptions := client.ListOptions{
			Namespace: UserNamespace,
		}
		deleteOptionsNamespace := client.DeleteAllOfOptions{
			ListOptions: deleteListOptions,
		}
		_ = k8sClient.DeleteAllOf(context.TODO(), &applicationapi.Component{}, &deleteOptionsNamespace)
		_ = k8sClient.DeleteAllOf(context.TODO(), &tektonapi.PipelineRun{}, &deleteOptionsNamespace)
		_ = k8sClient.DeleteAllOf(context.TODO(), &v1.ConfigMap{}, &deleteOptionsNamespace)

		eventList := v1.EventList{}
		err := k8sClient.List(context.TODO(), &eventList)
		Expect(err).ToNot(HaveOccurred())
		for i := range eventList.Items {
			_ = k8sClient.Delete(context.TODO(), &eventList.Items[i])
		}
	})

	Context("Test build pipelines have finalizer behaviour", func() {
		It("Test finalizer added and removed on deletion", func() {
			createBuildPipelineRun("test-pipeline-1", UserNamespace, BaseComponent)
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			pr := getPipelineRun("test-pipeline-1", UserNamespace)
			err := k8sClient.Delete(context.TODO(), pr)
			Expect(err).ToNot(HaveOccurred())
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return pr == nil
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

		})

		It("Test finalizer removed on pipeline completion", func() {
			createBuildPipelineRun("test-pipeline-1", UserNamespace, BaseComponent)
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			pr := getPipelineRun("test-pipeline-1", UserNamespace)
			pr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "False",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			pr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			Expect(k8sClient.Status().Update(ctx, pr)).Should(BeNil())
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return !controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

		})

		It("Test finalizer removed if component deleted", func() {
			createBuildPipelineRun("test-pipeline-2", UserNamespace, BaseComponent)
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-2", UserNamespace)
				return controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			componentList := applicationapi.ComponentList{}
			err := k8sClient.List(context.TODO(), &componentList)
			Expect(err).ToNot(HaveOccurred())
			for i := range componentList.Items {
				err = k8sClient.Delete(context.TODO(), &componentList.Items[i])
				Expect(err).ToNot(HaveOccurred())
			}
			// Deleting the components prunes the PipelineRuns

			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-2", UserNamespace)
				return pr == nil
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
		})
	})

	Context("Test build nudges component", func() {
		It("Test build performs nudge on success", func() {
			createBuildPipelineRun("test-pipeline-1", UserNamespace, BaseComponent)
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			pr := getPipelineRun("test-pipeline-1", UserNamespace)
			pr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			pr.Status.Results = []tektonapi.PipelineRunResult{
				{Name: ImageDigestParamName, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "sha256:12345"}},
				{Name: ImageUrl, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "quay.io.foo/bar:latest"}},
			}
			pr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			Expect(k8sClient.Status().Update(ctx, pr)).Should(BeNil())

			Eventually(func() bool {
				// check that no nudgeerror event was reported
				failureCount := getRenovateFailedEventCount()
				// check that renovate config was created
				renovateConfigsCreated := len(getRenovateConfigMapList())
				// check that renovate pipeline run was created
				renovatePipelinesCreated := getRenovatePipelineRunCount()
				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 1 && failureCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			renovateConfigMaps := getRenovateConfigMapList()
			Expect(len(renovateConfigMaps)).Should(Equal(1))
			for _, renovateConfig := range renovateConfigMaps {
				for _, renovateConfigData := range renovateConfig.Data {
					Expect(strings.Contains(renovateConfigData, `"username": "image_repo_username"`)).Should(BeTrue())
				}
			}
		})

		It("Test build performs nudge on success, image repository partial auth", func() {
			imageRepoSecretKey := types.NamespacedName{Name: "dockerconfigjsonsecret", Namespace: UserNamespace}
			// create secret with only partial repository match
			dockerConfigJson := fmt.Sprintf(`{"auths":{"quay.io":{"auth":"%s"}}}`, base64.StdEncoding.EncodeToString([]byte("image_repo_username_partial:image_repo_password")))
			imageRepoSecretData := map[string]string{
				".dockerconfigjson": dockerConfigJson,
			}
			deleteSecret(imageRepoSecretKey)
			createSCMSecret(imageRepoSecretKey, imageRepoSecretData, v1.SecretTypeDockerConfigJson, map[string]string{})

			serviceAccountKey := types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: UserNamespace}
			sa := waitServiceAccount(serviceAccountKey)
			sa.Secrets = []v1.ObjectReference{{Name: "dockerconfigjsonsecret"}}
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())

			createBuildPipelineRun("test-pipeline-1", UserNamespace, BaseComponent)
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			pr := getPipelineRun("test-pipeline-1", UserNamespace)
			pr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			pr.Status.Results = []tektonapi.PipelineRunResult{
				{Name: ImageDigestParamName, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "sha256:12345"}},
				{Name: ImageUrl, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "quay.io.foo/bar:latest"}},
			}
			pr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			Expect(k8sClient.Status().Update(ctx, pr)).Should(BeNil())

			Eventually(func() bool {
				// check that no nudgeerror event was reported
				failureCount := getRenovateFailedEventCount()
				// check that renovate config was created
				renovateConfigsCreated := len(getRenovateConfigMapList())
				// check that renovate pipeline run was created
				renovatePipelinesCreated := getRenovatePipelineRunCount()
				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 1 && failureCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			renovateConfigMaps := getRenovateConfigMapList()
			Expect(len(renovateConfigMaps)).Should(Equal(1))
			for _, renovateConfig := range renovateConfigMaps {
				for _, renovateConfigData := range renovateConfig.Data {
					Expect(strings.Contains(renovateConfigData, `"username": "image_repo_username_partial"`)).Should(BeTrue())
				}
			}
		})

		It("Test build performs nudge on success, image repository partial auth from multiple secrets", func() {
			// create secret with one partial repository match
			dockerConfigJson := fmt.Sprintf(`{"auths":{"quay.io":{"auth":"%s"}}}`, base64.StdEncoding.EncodeToString([]byte("image_repo_username_1:image_repo_password")))
			imageRepoSecretData := map[string]string{
				".dockerconfigjson": dockerConfigJson,
			}
			imageRepoSecretKey := types.NamespacedName{Name: "dockerconfigjsonsecret", Namespace: UserNamespace}
			deleteSecret(imageRepoSecretKey)
			createSCMSecret(imageRepoSecretKey, imageRepoSecretData, v1.SecretTypeDockerConfigJson, map[string]string{})

			// create secret with another partial repository match, this one will be used (because has more complete path)
			dockerConfigJson = fmt.Sprintf(`{"auths":{"quay.io/organization":{"auth":"%s"}}}`, base64.StdEncoding.EncodeToString([]byte("image_repo_username_2:image_repo_password")))
			imageRepoSecretData = map[string]string{
				".dockerconfigjson": dockerConfigJson,
			}
			imageRepoSecretKey = types.NamespacedName{Name: "dockerconfigjsonsecret2", Namespace: UserNamespace}
			createSCMSecret(imageRepoSecretKey, imageRepoSecretData, v1.SecretTypeDockerConfigJson, map[string]string{})

			// create secret with path which doesn't match at all
			dockerConfigJson = fmt.Sprintf(`{"auths":{"registry.io":{"auth":"%s"}}}`, base64.StdEncoding.EncodeToString([]byte("image_repo_username_3:image_repo_password")))
			imageRepoSecretData = map[string]string{
				".dockerconfigjson": dockerConfigJson,
			}
			imageRepoSecretKey = types.NamespacedName{Name: "dockerconfigjsonsecret3", Namespace: UserNamespace}
			createSCMSecret(imageRepoSecretKey, imageRepoSecretData, v1.SecretTypeDockerConfigJson, map[string]string{})

			serviceAccountKey := types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: UserNamespace}
			sa := waitServiceAccount(serviceAccountKey)
			sa.Secrets = []v1.ObjectReference{{Name: "dockerconfigjsonsecret"}, {Name: "dockerconfigjsonsecret2"}}
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())

			createBuildPipelineRun("test-pipeline-1", UserNamespace, BaseComponent)
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			pr := getPipelineRun("test-pipeline-1", UserNamespace)
			pr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			pr.Status.Results = []tektonapi.PipelineRunResult{
				{Name: ImageDigestParamName, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "sha256:12345"}},
				{Name: ImageUrl, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "quay.io.foo/bar:latest"}},
			}
			pr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			Expect(k8sClient.Status().Update(ctx, pr)).Should(BeNil())

			Eventually(func() bool {
				// check that no nudgeerror event was reported
				failureCount := getRenovateFailedEventCount()
				// check that renovate config was created
				renovateConfigsCreated := len(getRenovateConfigMapList())
				// check that renovate pipeline run was created
				renovatePipelinesCreated := getRenovatePipelineRunCount()
				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 1 && failureCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			renovateConfigMaps := getRenovateConfigMapList()
			Expect(len(renovateConfigMaps)).Should(Equal(1))
			for _, renovateConfig := range renovateConfigMaps {
				for _, renovateConfigData := range renovateConfig.Data {
					Expect(strings.Contains(renovateConfigData, `"username": "image_repo_username_2"`)).Should(BeTrue())
				}
			}
		})

		It("Test stale pipeline not nudged", func() {
			createBuildPipelineRun("stale-pipeline", UserNamespace, BaseComponent)
			time.Sleep(time.Second + time.Millisecond)
			createBuildPipelineRun("test-pipeline-1", UserNamespace, BaseComponent)
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			pr := getPipelineRun("test-pipeline-1", UserNamespace)
			pr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			pr.Status.Results = []tektonapi.PipelineRunResult{
				{Name: ImageDigestParamName, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "sha256:12345"}},
				{Name: ImageUrl, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "quay.io.foo/bar:latest"}},
			}
			pr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			Expect(k8sClient.Status().Update(ctx, pr)).Should(BeNil())

			Eventually(func() bool {
				//test that the state pipeline run was marked as nudged, even though it has not completed
				stale := getPipelineRun("stale-pipeline", UserNamespace)
				if stale.Annotations == nil || stale.Annotations[NudgeProcessedAnnotationName] != "true" || controllerutil.ContainsFinalizer(stale, NudgeFinalizer) {
					return false
				}
				failureCount := getRenovateFailedEventCount()
				// check that renovate config was created
				renovateConfigsCreated := len(getRenovateConfigMapList())
				// check that renovate pipeline run was created
				renovatePipelinesCreated := getRenovatePipelineRunCount()

				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 1 && failureCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
		})

	})

	Context("Test nudge failure handling", func() {
		It("Test single failure results in retry", func() {
			failures = 1
			// mock function to produce error
			GenerateRenovateConfigForNudge = func(target updateTarget, buildResult *BuildResult) (string, error) {
				if failures == 0 {
					log.Info("components nudged")
					return "\nrenovate_config\nrenovate_line1\nrenovate_line2\n", nil
				}
				failures = failures - 1
				return "", fmt.Errorf("failure")
			}

			createBuildPipelineRun("test-pipeline-1", UserNamespace, BaseComponent)
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			pr := getPipelineRun("test-pipeline-1", UserNamespace)
			pr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			pr.Status.Results = []tektonapi.PipelineRunResult{
				{Name: ImageDigestParamName, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "sha256:12345"}},
				{Name: ImageUrl, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "quay.io.foo/bar:latest"}},
			}
			pr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			Expect(k8sClient.Status().Update(ctx, pr)).Should(BeNil())

			// wait for one nudge error event
			Eventually(func() bool {
				failureCount := getRenovateFailedEventCount()
				return failureCount == 1
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			// after one error it will retry and create pipeline
			Eventually(func() bool {
				// check that renovate config was created
				renovateConfigsCreated := len(getRenovateConfigMapList())
				// check that renovate pipeline run was created
				renovatePipelinesCreated := getRenovatePipelineRunCount()

				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 1
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
		})

		It("Test retries exceeded", func() {
			failures = 10
			// mock function to produce error
			GenerateRenovateConfigForNudge = func(target updateTarget, buildResult *BuildResult) (string, error) {
				if failures == 0 {
					log.Info("components nudged")
					return "\nrenovate_config\nrenovate_line1\nrenovate_line2\n", nil
				}
				failures = failures - 1
				return "", fmt.Errorf("failure")
			}

			createBuildPipelineRun("test-pipeline-1", UserNamespace, BaseComponent)
			Eventually(func() bool {
				pr := getPipelineRun("test-pipeline-1", UserNamespace)
				return controllerutil.ContainsFinalizer(pr, NudgeFinalizer)
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			pr := getPipelineRun("test-pipeline-1", UserNamespace)
			pr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			pr.Status.Results = []tektonapi.PipelineRunResult{
				{Name: ImageDigestParamName, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "sha256:12345"}},
				{Name: ImageUrl, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "quay.io.foo/bar:latest"}},
			}
			pr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			Expect(k8sClient.Status().Update(ctx, pr)).Should(BeNil())

			Eventually(func() bool {
				// check that multiple nudgeerror event were reported
				failureCount := getRenovateFailedEventCount()
				// check that no renovate pipeline run was created
				renovatePipelinesCreated := getRenovatePipelineRunCount()

				return renovatePipelinesCreated == 0 && failureCount == 3
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
		})
	})

	Context("Test renovate config template", func() {
		It("test template output", func() {
			componentName := "test-component"
			component := &applicationapi.Component{}
			component.Name = componentName

			gitProvider := "github"
			gitUsername := "renovate-username"
			gitAuthor := "renovate-gitauthor"
			gitToken := "renovate-token"
			gitEndpoint := "https://api.github.com/"
			imageRepositoryHost := "quay.io"
			imageRepositoryUsername := "repository_username"
			fileMatches := "file1, file2, file3"
			fileMatchesList := `["file1","file2","file3"]`
			repositories := []renovateRepository{{BaseBranches: []string{"base_branch"}, Repository: "some_repository/something"}}
			repositoriesData, _ := json.Marshal(repositories)
			buildImageRepository := "quay.io/testorg/testimage"
			builtImageTag := "a8dce08dbdf290e5d616a83672ad3afcb4b455ef"
			digest := "sha256:716be32f12f0dd31adbab1f57e9d0b87066e03de51c89dce8ffb397fbac92314"
			distributionRepositories := []string{"registry.redhat.com/some-product", "registry.redhat.com/other-product"}

			buildResult := BuildResult{
				BuiltImageRepository:     buildImageRepository,
				BuiltImageTag:            builtImageTag,
				Digest:                   digest,
				Component:                component,
				DistributionRepositories: distributionRepositories,
				FileMatches:              fileMatches,
			}
			renovateTarget := updateTarget{
				ComponentName:           "nudged component",
				GitProvider:             gitProvider,
				Username:                gitUsername,
				GitAuthor:               gitAuthor,
				Token:                   gitToken,
				Endpoint:                gitEndpoint,
				Repositories:            repositories,
				ImageRepositoryHost:     imageRepositoryHost,
				ImageRepositoryUsername: imageRepositoryUsername,
			}

			result, err := generateRenovateConfigForNudge(renovateTarget, &buildResult)
			Expect(err).Should(Succeed())

			Expect(strings.Contains(result, fmt.Sprintf(`platform: "%s"`, gitProvider))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`username: "%s"`, gitUsername))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`gitAuthor: "%s"`, gitAuthor))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`repositories: %s`, repositoriesData))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`"username": "%s"`, imageRepositoryUsername))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`"matchHost": "%s"`, imageRepositoryHost))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`endpoint: "%s"`, gitEndpoint))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`"fileMatch": %s`, fileMatchesList))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`"currentValueTemplate": "%s"`, buildResult.BuiltImageTag))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`"depNameTemplate": "%s"`, buildImageRepository))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`groupName: "Component Update %s"`, componentName))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`branchName: "konflux/component-updates/%s"`, componentName))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`commitMessageTopic: "%s"`, componentName))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`followTag: "%s"`, builtImageTag))).Should(BeTrue())
			Expect(strings.Contains(result, fmt.Sprintf(`"matchPackageNames": ["%s","%s","%s"]`, buildImageRepository, distributionRepositories[0], distributionRepositories[1]))).Should(BeTrue())
			registryAlias1 := fmt.Sprintf(`"%s": "%s",`, distributionRepositories[0], buildImageRepository)
			registryAlias2 := fmt.Sprintf(`"%s": "%s"`, distributionRepositories[1], buildImageRepository)
			Expect(strings.Contains(result, registryAlias1)).Should(BeTrue())
			Expect(strings.Contains(result, registryAlias2)).Should(BeTrue())
		})
	})

	Context("Test mapping quay.io to registry.redhat.io", func() {
		It("test regex match", func() {
			Expect(mapToRegistryRedhatIo("quay.io/redhat-prod/multiarch-tuning----multiarch-tuning-rhel9-operator")).To(Equal("registry.redhat.io/multiarch-tuning/multiarch-tuning-rhel9-operator"))
			Expect(mapToRegistryRedhatIo("quay.io/redhat-pending/multiarch-tuning----multiarch-tuning-rhel9-operator")).To(Equal("registry.stage.redhat.io/multiarch-tuning/multiarch-tuning-rhel9-operator"))
		})
	})
})

func getPipelineRun(name string, namespace string) *tektonapi.PipelineRun {
	pr := tektonapi.PipelineRun{}
	err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, &pr)
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	Expect(err).ToNot(HaveOccurred())
	return &pr
}

func createBuildPipelineRun(name string, namespace string, component string) *tektonapi.PipelineRun {
	pipelineSpec := &tektonapi.PipelineSpec{
		Results: []tektonapi.PipelineResult{
			{Name: ImageUrl, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "$(tasks.build-container.results.IMAGE_URL)"}},
			{Name: ImageDigest, Value: tektonapi.ResultValue{Type: tektonapi.ParamTypeString, StringVal: "$(tasks.build-container.results.IMAGE_DIGEST)"}},
		},
		Tasks: []tektonapi.PipelineTask{
			{
				Name: "build-container",
				TaskSpec: &tektonapi.EmbeddedTask{
					TaskSpec: tektonapi.TaskSpec{
						Results: []tektonapi.TaskResult{{Name: ImageUrl, Type: tektonapi.ResultsTypeString}, {Name: ImageDigest, Type: tektonapi.ResultsTypeString}},
						Steps: []tektonapi.Step{
							{
								Name:   "buildah",
								Image:  "quay.io/buildah/fakebuildaimage:latest",
								Script: "echo hello",
							},
						},
					},
				},
			},
		},
	}
	run := tektonapi.PipelineRun{}
	run.Labels = map[string]string{ComponentNameLabelName: component, PipelineRunTypeLabelName: PipelineRunBuildType}
	run.Annotations = map[string]string{PacEventTypeAnnotationName: PacEventPushType, NudgeFilesAnnotationName: ".*Dockerfile.*, .*.yaml, .*Containerfile.*"}
	run.Namespace = namespace
	run.Name = name
	run.Spec.PipelineSpec = pipelineSpec
	err := k8sClient.Create(context.TODO(), &run)
	Expect(err).ToNot(HaveOccurred())
	return &run
}

func getRenovateConfigMapList() []v1.ConfigMap {
	configMapList := &v1.ConfigMapList{}
	renovateConfigMapList := []v1.ConfigMap{}
	err := k8sClient.List(context.TODO(), configMapList, &client.ListOptions{Namespace: UserNamespace})
	Expect(err).ToNot(HaveOccurred())
	for _, configMap := range configMapList.Items {
		if strings.HasPrefix(configMap.ObjectMeta.Name, "renovate-pipeline") {
			log.Info(configMap.ObjectMeta.Name)
			renovateConfigMapList = append(renovateConfigMapList, configMap)
		}
	}
	return renovateConfigMapList
}

func getRenovatePipelineRunCount() int {
	renovatePipelinesCreated := 0
	prList := &tektonapi.PipelineRunList{}
	err := k8sClient.List(context.TODO(), prList, &client.ListOptions{Namespace: UserNamespace})
	Expect(err).ToNot(HaveOccurred())
	for _, pipelineRun := range prList.Items {
		if strings.HasPrefix(pipelineRun.ObjectMeta.Name, "renovate-pipeline") {
			log.Info(pipelineRun.ObjectMeta.Name)
			renovatePipelinesCreated += 1
		}
	}
	return renovatePipelinesCreated
}

func getRenovateFailedEventCount() int {
	failureCount := 0
	events := v1.EventList{}
	err := k8sClient.List(context.TODO(), &events)
	Expect(err).ToNot(HaveOccurred())
	for _, i := range events.Items {
		if i.Reason == ComponentNudgeFailedEventType {
			failureCount++

		}
	}
	return failureCount
}
