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
	ImageUrl      = "IMAGE_URL"
	ImageDigest   = "IMAGE_DIGEST"

	buildStatusPaCProvisionedValue = `{"pac":{"state":"enabled","merge-url":"https://githost.com/org/repo/pull/123","configuration-time":"Wed, 06 Nov 2024 08:41:44 UTC"},"message":"done"}`
)

var failures = 0

var _ = Describe("Component nudge controller", func() {

	delayTime = 0
	BeforeEach(func() {
		createNamespace(UserNamespace)
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

		caConfigMapKey := types.NamespacedName{Name: "trusted-ca", Namespace: BuildServiceNamespaceName}
		createCAConfigMap(caConfigMapKey)

		createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

		github.GetAppInstallationsForRepository = func(githubAppIdStr string, appPrivateKeyPem []byte, repoUrl string) (*github.ApplicationInstallation, string, error) {
			repo_name := "repo_name"
			repo_fullname := "repo_fullname"
			repo_defaultbranch := "repo_defaultbranch"
			return &github.ApplicationInstallation{
				Token:        "some_token",
				ID:           1,
				Repositories: []*gitgithub.Repository{{Name: &repo_name, FullName: &repo_fullname, DefaultBranch: &repo_defaultbranch}},
			}, TestGitHubAppName, nil
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
		var (
			baseComponentName = "base-component-finalizer"
			baseComponentKey  = types.NamespacedName{Namespace: UserNamespace, Name: baseComponentName}
			operator1Key      = types.NamespacedName{Namespace: UserNamespace, Name: "operator1-finalizer"}
			operator2Key      = types.NamespacedName{Namespace: UserNamespace, Name: "operator2-finalizer"}
		)

		_ = BeforeEach(func() {
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey:   baseComponentKey,
				containerImage: "quay.io/organization/repo:tag",
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done, so when we update BuildNudgesRef there won't be any new object
				},
			})
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey:   operator1Key,
				containerImage: "quay.io/organization/repo1:tag",
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done
				},
			})
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey:   operator2Key,
				containerImage: "quay.io/organization/repo2:tag",
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done
				},
			})
			baseComponent := applicationapi.Component{}
			err := k8sClient.Get(context.TODO(), baseComponentKey, &baseComponent)
			Expect(err).ToNot(HaveOccurred())
			baseComponent.Spec.BuildNudgesRef = []string{operator1Key.Name, operator2Key.Name}
			err = k8sClient.Update(context.TODO(), &baseComponent)
			Expect(err).ToNot(HaveOccurred())

			serviceAccountKey := getComponentServiceAccountKey(baseComponentKey)
			sa := waitServiceAccount(serviceAccountKey)
			sa.Secrets = []v1.ObjectReference{{Name: "dockerconfigjsonsecret"}}
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())
		})

		It("Test finalizer added and removed on deletion", func() {
			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, "")
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
			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, "")
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
			createBuildPipelineRun("test-pipeline-2", UserNamespace, baseComponentName, "")
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
		var (
			baseComponentName = "base-component-nudges"
			baseComponentKey  = types.NamespacedName{Namespace: UserNamespace, Name: baseComponentName}
			operator1Key      = types.NamespacedName{Namespace: UserNamespace, Name: "operator1-nudges"}
			operator2Key      = types.NamespacedName{Namespace: UserNamespace, Name: "operator2-nudges"}
		)

		_ = BeforeEach(func() {
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey:   baseComponentKey,
				containerImage: "quay.io/organization/repo:tag",
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done, so when we update BuildNudgesRef there won't be any new object
				},
			})
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey:   operator1Key,
				containerImage: "quay.io/organization/repo1:tag",
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done
				},
			})
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey:   operator2Key,
				containerImage: "quay.io/organization/repo2:tag",
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done
				},
			})
			baseComponent := applicationapi.Component{}
			err := k8sClient.Get(context.TODO(), baseComponentKey, &baseComponent)
			Expect(err).ToNot(HaveOccurred())
			baseComponent.Spec.BuildNudgesRef = []string{operator1Key.Name, operator2Key.Name}
			err = k8sClient.Update(context.TODO(), &baseComponent)
			Expect(err).ToNot(HaveOccurred())

			serviceAccountKey := getComponentServiceAccountKey(baseComponentKey)
			sa := waitServiceAccount(serviceAccountKey)
			sa.Secrets = []v1.ObjectReference{{Name: "dockerconfigjsonsecret"}}
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())
		})

		It("Test build performs nudge on success", func() {
			rpaKey := types.NamespacedName{Namespace: UserNamespace, Name: "testing-rpa"}
			createReleasePlanAdmission(rpaKey, baseComponentName)

			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, "")
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
				renovatePipelinesCreated := len(getRenovatePipelineRunList())
				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 2 && failureCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			renovateConfigMaps := getRenovateConfigMapList()
			Expect(len(renovateConfigMaps)).Should(Equal(2))
			renovateConfigMapFound := false
			renovateCaConfigMapFound := false

			for _, renovateConfig := range renovateConfigMaps {
				if strings.HasPrefix(renovateConfig.ObjectMeta.Name, "renovate-pipeline") {
					renovateConfigMapFound = true
					// verify that repositories from RPA mapping are used
					for _, renovateJsonData := range renovateConfig.Data {
						Expect(strings.Contains(renovateJsonData, rpaMappingRepository1)).Should(BeTrue())
						Expect(strings.Contains(renovateJsonData, rpaMappingRepository2)).Should(BeTrue())
						Expect(strings.Contains(renovateJsonData, rpaMappingRepository3)).Should(BeTrue())
					}
				}
				if strings.HasPrefix(renovateConfig.ObjectMeta.Name, "renovate-ca") {
					renovateCaConfigMapFound = true
					Expect(renovateConfig.Data).Should(Equal(map[string]string{CaConfigMapKey: "someCAcertdata"}))
				}
			}
			Expect(renovateCaConfigMapFound && renovateConfigMapFound).Should(BeTrue())

			renovatePipelines := getRenovatePipelineRunList()
			Expect(len(renovatePipelines)).Should(Equal(1))
			renovateCommand := strings.Join(renovatePipelines[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Command, ";")
			Expect(strings.Contains(renovateCommand, `'username':'image_repo_username'`)).Should(BeTrue())
			caMounted := false
			for _, volumeMount := range renovatePipelines[0].Spec.PipelineSpec.Tasks[0].TaskSpec.TaskSpec.Steps[0].VolumeMounts {
				if volumeMount.Name == CaVolumeMountName {
					caMounted = true
					break
				}
			}
			Expect(caMounted).Should(BeTrue())
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

			serviceAccountKey := getComponentServiceAccountKey(baseComponentKey)
			sa := waitServiceAccount(serviceAccountKey)
			sa.Secrets = []v1.ObjectReference{{Name: "dockerconfigjsonsecret"}}
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())

			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, "")
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
				renovatePipelinesCreated := len(getRenovatePipelineRunList())
				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 2 && failureCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			renovatePipelines := getRenovatePipelineRunList()
			Expect(len(renovatePipelines)).Should(Equal(1))
			renovateCommand := strings.Join(renovatePipelines[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Command, ";")
			Expect(strings.Contains(renovateCommand, `'username':'image_repo_username_partial'`)).Should(BeTrue())
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

			serviceAccountKey := getComponentServiceAccountKey(baseComponentKey)
			sa := waitServiceAccount(serviceAccountKey)
			sa.Secrets = []v1.ObjectReference{{Name: "dockerconfigjsonsecret"}, {Name: "dockerconfigjsonsecret2"}}
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())

			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, "")
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
				renovatePipelinesCreated := len(getRenovatePipelineRunList())
				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 2 && failureCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			renovatePipelines := getRenovatePipelineRunList()
			Expect(len(renovatePipelines)).Should(Equal(1))
			renovateCommand := strings.Join(renovatePipelines[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Command, ";")
			Expect(strings.Contains(renovateCommand, `'username':'image_repo_username_2'`)).Should(BeTrue())
		})

		It("Test stale pipeline not nudged", func() {
			createBuildPipelineRun("stale-pipeline", UserNamespace, baseComponentName, "")
			time.Sleep(time.Second + time.Millisecond)
			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, "")
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
				renovatePipelinesCreated := len(getRenovatePipelineRunList())

				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 2 && failureCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
		})

		var assertRenovateConfiMap = func(componentRenovateConfigName *types.NamespacedName, componentRenovateConfigData, namespaceRenovateConfigData map[string]string, imageBuiltFrom string) {
			customNamespaceConfigMapName := types.NamespacedName{Namespace: UserNamespace, Name: NamespaceWideRenovateConfigMapName}
			if namespaceRenovateConfigData != nil {
				createCustomRenovateConfigMap(customNamespaceConfigMapName, namespaceRenovateConfigData)
			}

			if componentRenovateConfigName != nil && componentRenovateConfigData != nil {
				createCustomRenovateConfigMap(*componentRenovateConfigName, componentRenovateConfigData)

				component1 := applicationapi.Component{}
				component2 := applicationapi.Component{}
				err := k8sClient.Get(context.TODO(), operator1Key, &component1)
				Expect(err).ToNot(HaveOccurred())
				err = k8sClient.Get(context.TODO(), operator2Key, &component2)
				Expect(err).ToNot(HaveOccurred())

				if component1.Annotations == nil {
					component1.Annotations = make(map[string]string)
				}
				if component2.Annotations == nil {
					component2.Annotations = make(map[string]string)
				}

				// add annotation with custom renovate config map
				component1.Annotations[CustomRenovateConfigMapAnnotation] = componentRenovateConfigName.Name
				err = k8sClient.Update(context.TODO(), &component1)
				Expect(err).ToNot(HaveOccurred())
				waitComponentAnnotationExists(operator1Key, CustomRenovateConfigMapAnnotation)
				component2.Annotations[CustomRenovateConfigMapAnnotation] = componentRenovateConfigName.Name
				err = k8sClient.Update(context.TODO(), &component2)
				Expect(err).ToNot(HaveOccurred())
				waitComponentAnnotationExists(operator2Key, CustomRenovateConfigMapAnnotation)
			}

			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, imageBuiltFrom)
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
				renovatePipelinesCreated := len(getRenovatePipelineRunList())
				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 2 && failureCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			if namespaceRenovateConfigData != nil {
				deleteConfigMap(customNamespaceConfigMapName)
			}
			if componentRenovateConfigName != nil && componentRenovateConfigData != nil {
				deleteConfigMap(*componentRenovateConfigName)
			}
		}

		It("Test build performs nudge on success, only namespace wide renovate config provided", func() {
			customConfigType := "json"
			automergeValue := true
			automergeTypeValue := "pr"
			platformAutomergeValue := false
			ignoreTestsValue := true
			commitMessagePrefixValue := "msg_prefix"
			commitMessageSuffixValue := "msg_suffix"
			gitLabIgnoreApprovalsValue := true
			automergeScheduleValue := []string{"* 22-23,0-4 * * *", "* * * * 0,6"}
			imageBuiltFrom := "repo/sha"
			commitBodyValue := fmt.Sprintf("Image created from '%s'\n\nSigned-off-by:", imageBuiltFrom)
			fileMatchValue := []string{"cfile1", "cfile2"}
			labelsValue := []string{DefaultRenovateLabel, "custom1", "custom2"}
			customConfigMapData := map[string]string{
				RenovateConfigMapAutomergeKey:             "true",
				RenovateConfigMapCommitMessagePrefixKey:   commitMessagePrefixValue,
				RenovateConfigMapCommitMessageSuffixKey:   commitMessageSuffixValue,
				RenovateConfigMapFileMatchKey:             "cfile1, cfile2",
				RenovateConfigMapAutomergeTypeKey:         "pr",
				RenovateConfigMapPlatformAutomergeKey:     "false",
				RenovateConfigMapIgnoreTestsKey:           "true",
				RenovateConfigMapGitLabIgnoreApprovalsKey: "true",
				RenovateConfigMapAutomergeScheduleKey:     "* 22-23,0-4 * * *; * * * * 0,6",
				RenovateConfigMapLabelsKey:                "custom1, custom2",
			}
			gitHubAppUsername := fmt.Sprintf("%s[bot]", TestGitHubAppName)
			gitHubAppGitAuthor := fmt.Sprintf("%s <%d+%s@users.noreply.github.com>", TestGitHubAppName, TestGitHubAppId, gitHubAppUsername)
			gitHubAppSignedOff := fmt.Sprintf("Signed-off-by: %s", gitHubAppGitAuthor)
			gitHubAppUsed := 0
			assertRenovateConfiMap(nil, nil, customConfigMapData, imageBuiltFrom)

			renovateConfigMaps := getRenovateConfigMapList()
			Expect(len(renovateConfigMaps)).Should(Equal(2))
			for _, renovateConfig := range renovateConfigMaps {
				if strings.HasPrefix(renovateConfig.ObjectMeta.Name, "renovate-pipeline") {
					for key, val := range renovateConfig.Data {
						renovateConfigObj := &RenovateConfig{}
						err := json.Unmarshal([]byte(val), renovateConfigObj)
						Expect(err).ToNot(HaveOccurred())
						Expect(renovateConfigObj.Automerge).Should(Equal(automergeValue))
						Expect(renovateConfigObj.AutomergeType).Should(Equal(automergeTypeValue))
						Expect(renovateConfigObj.PlatformAutomerge).Should(Equal(platformAutomergeValue))
						Expect(renovateConfigObj.IgnoreTests).Should(Equal(ignoreTestsValue))
						Expect(renovateConfigObj.GitLabIgnoreApprovals).Should(Equal(gitLabIgnoreApprovalsValue))
						Expect(renovateConfigObj.AutomergeSchedule).Should(Equal(automergeScheduleValue))
						Expect(renovateConfigObj.Labels).Should(Equal(labelsValue))
						Expect(renovateConfigObj.PackageRules[1].CommitMessagePrefix).Should(Equal(commitMessagePrefixValue))
						Expect(renovateConfigObj.PackageRules[1].CommitMessageSuffix).Should(Equal(commitMessageSuffixValue))
						Expect(strings.Contains(renovateConfigObj.PackageRules[1].CommitBody, commitBodyValue)).Should(BeTrue())
						Expect(renovateConfigObj.CustomManagers[0].FileMatch).Should(Equal(fileMatchValue))
						Expect(strings.HasSuffix(key, customConfigType))
						if renovateConfigObj.Username == gitHubAppUsername {
							gitHubAppUsed++
							Expect(renovateConfigObj.GitAuthor).Should(Equal(gitHubAppGitAuthor))
							Expect(strings.Contains(renovateConfigObj.PackageRules[1].CommitBody, gitHubAppSignedOff)).Should(BeTrue())
						}
					}
					break
				}
			}
			Expect(gitHubAppUsed).Should(Equal(2))
		})

		It("Test build performs nudge on success, only component renovate config provided", func() {
			customConfigType := "json"
			customConfigName := "component-specific-renovate-config"
			customConfigMapName := types.NamespacedName{Namespace: UserNamespace, Name: customConfigName}
			automergeValue := true
			automergeTypeValue := "pr"
			platformAutomergeValue := false
			ignoreTestsValue := true
			commitMessagePrefixValue := "msg_prefix"
			commitMessageSuffixValue := "msg_suffix"
			gitLabIgnoreApprovalsValue := true
			automergeScheduleValue := []string{"* 22-23,0-4 * * *", "* * * * 0,6"}
			imageBuiltFrom := "repo/sha"
			commitBodyValue := fmt.Sprintf("Image created from '%s'\n\nSigned-off-by:", imageBuiltFrom)
			fileMatchValue := []string{"cfile1", "cfile2"}
			labelsValue := []string{DefaultRenovateLabel, "custom1", "custom2"}
			customConfigMapData := map[string]string{
				RenovateConfigMapAutomergeKey:             "true",
				RenovateConfigMapCommitMessagePrefixKey:   commitMessagePrefixValue,
				RenovateConfigMapCommitMessageSuffixKey:   commitMessageSuffixValue,
				RenovateConfigMapFileMatchKey:             "cfile1, cfile2",
				RenovateConfigMapAutomergeTypeKey:         "pr",
				RenovateConfigMapPlatformAutomergeKey:     "false",
				RenovateConfigMapIgnoreTestsKey:           "true",
				RenovateConfigMapGitLabIgnoreApprovalsKey: "true",
				RenovateConfigMapAutomergeScheduleKey:     "* 22-23,0-4 * * *; * * * * 0,6",
				RenovateConfigMapLabelsKey:                "custom1, custom2",
			}
			assertRenovateConfiMap(&customConfigMapName, customConfigMapData, nil, imageBuiltFrom)

			renovateConfigMaps := getRenovateConfigMapList()
			Expect(len(renovateConfigMaps)).Should(Equal(2))
			for _, renovateConfig := range renovateConfigMaps {
				if strings.HasPrefix(renovateConfig.ObjectMeta.Name, "renovate-pipeline") {
					for key, val := range renovateConfig.Data {
						renovateConfigObj := &RenovateConfig{}
						err := json.Unmarshal([]byte(val), renovateConfigObj)
						Expect(err).ToNot(HaveOccurred())
						Expect(renovateConfigObj.Automerge).Should(Equal(automergeValue))
						Expect(renovateConfigObj.AutomergeType).Should(Equal(automergeTypeValue))
						Expect(renovateConfigObj.PlatformAutomerge).Should(Equal(platformAutomergeValue))
						Expect(renovateConfigObj.IgnoreTests).Should(Equal(ignoreTestsValue))
						Expect(renovateConfigObj.GitLabIgnoreApprovals).Should(Equal(gitLabIgnoreApprovalsValue))
						Expect(renovateConfigObj.AutomergeSchedule).Should(Equal(automergeScheduleValue))
						Expect(renovateConfigObj.Labels).Should(Equal(labelsValue))
						Expect(renovateConfigObj.PackageRules[1].CommitMessagePrefix).Should(Equal(commitMessagePrefixValue))
						Expect(renovateConfigObj.PackageRules[1].CommitMessageSuffix).Should(Equal(commitMessageSuffixValue))
						Expect(strings.Contains(renovateConfigObj.PackageRules[1].CommitBody, commitBodyValue)).Should(BeTrue())
						Expect(renovateConfigObj.CustomManagers[0].FileMatch).Should(Equal(fileMatchValue))
						Expect(strings.HasSuffix(key, customConfigType))
					}
					break
				}
			}
		})

		It("Test build performs nudge on success, namespace wide and component renovate config provided", func() {
			customConfigType := "json"
			customConfigName := "component-specific-renovate-config"
			customConfigMapName := types.NamespacedName{Namespace: UserNamespace, Name: customConfigName}
			automergeValue := true
			commitMessagePrefixValue := "msg_prefix"
			commitMessageSuffixValue := "msg_suffix"
			imageBuiltFrom := "repo/sha"
			commitBodyValue := fmt.Sprintf("Image created from '%s'\n\nSigned-off-by:", imageBuiltFrom)
			automergeTypeValue := "pr"
			platformAutomergeValue := false
			ignoreTestsValue := true
			gitLabIgnoreApprovalsValue := true
			automergeScheduleValue := []string{"* 22-23,0-4 * * *", "* * * * 0,6"}
			fileMatchValue := []string{"cfile1", "cfile2"}
			labelsValue := []string{DefaultRenovateLabel, "custom1", "custom2"}
			customConfigMapData := map[string]string{
				RenovateConfigMapAutomergeKey:             "true",
				RenovateConfigMapCommitMessagePrefixKey:   commitMessagePrefixValue,
				RenovateConfigMapCommitMessageSuffixKey:   commitMessageSuffixValue,
				RenovateConfigMapFileMatchKey:             "cfile1, cfile2",
				RenovateConfigMapAutomergeTypeKey:         "pr",
				RenovateConfigMapPlatformAutomergeKey:     "false",
				RenovateConfigMapIgnoreTestsKey:           "true",
				RenovateConfigMapGitLabIgnoreApprovalsKey: "true",
				RenovateConfigMapAutomergeScheduleKey:     "* 22-23,0-4 * * *; * * * * 0,6",
				RenovateConfigMapLabelsKey:                "custom1, custom2",
			}
			customNamespaceConfigMapData := map[string]string{
				RenovateConfigMapAutomergeKey:             "false",
				RenovateConfigMapCommitMessagePrefixKey:   "namespace_prefix",
				RenovateConfigMapCommitMessageSuffixKey:   "namespace_suffix",
				RenovateConfigMapFileMatchKey:             "namespace1, namespace2",
				RenovateConfigMapAutomergeTypeKey:         "namespacepr",
				RenovateConfigMapPlatformAutomergeKey:     "true",
				RenovateConfigMapIgnoreTestsKey:           "false",
				RenovateConfigMapGitLabIgnoreApprovalsKey: "false",
				RenovateConfigMapAutomergeScheduleKey:     "1 * * * *",
				RenovateConfigMapLabelsKey:                "namespacecustom1, namespacecustom2",
			}
			assertRenovateConfiMap(&customConfigMapName, customConfigMapData, customNamespaceConfigMapData, imageBuiltFrom)

			renovateConfigMaps := getRenovateConfigMapList()
			Expect(len(renovateConfigMaps)).Should(Equal(2))
			for _, renovateConfig := range renovateConfigMaps {
				if strings.HasPrefix(renovateConfig.ObjectMeta.Name, "renovate-pipeline") {
					for key, val := range renovateConfig.Data {
						renovateConfigObj := &RenovateConfig{}
						err := json.Unmarshal([]byte(val), renovateConfigObj)
						Expect(err).ToNot(HaveOccurred())
						Expect(renovateConfigObj.Automerge).Should(Equal(automergeValue))
						Expect(renovateConfigObj.AutomergeType).Should(Equal(automergeTypeValue))
						Expect(renovateConfigObj.PlatformAutomerge).Should(Equal(platformAutomergeValue))
						Expect(renovateConfigObj.IgnoreTests).Should(Equal(ignoreTestsValue))
						Expect(renovateConfigObj.GitLabIgnoreApprovals).Should(Equal(gitLabIgnoreApprovalsValue))
						Expect(renovateConfigObj.AutomergeSchedule).Should(Equal(automergeScheduleValue))
						Expect(renovateConfigObj.Labels).Should(Equal(labelsValue))
						Expect(renovateConfigObj.PackageRules[1].CommitMessagePrefix).Should(Equal(commitMessagePrefixValue))
						Expect(renovateConfigObj.PackageRules[1].CommitMessageSuffix).Should(Equal(commitMessageSuffixValue))
						Expect(strings.Contains(renovateConfigObj.PackageRules[1].CommitBody, commitBodyValue)).Should(BeTrue())
						Expect(renovateConfigObj.CustomManagers[0].FileMatch).Should(Equal(fileMatchValue))
						Expect(strings.HasSuffix(key, customConfigType))
					}
					break
				}
			}
		})

		It("Should use dedicated build pipeline Service Account in renovate pipeline", func() {
			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, "")
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
			Expect(k8sClient.Status().Update(ctx, pr)).To(Succeed())

			Eventually(func() bool {
				// check that renovate pipeline run was created
				renovatePipelinesCreated := len(getRenovatePipelineRunList())
				return renovatePipelinesCreated == 1
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			renovatePipelines := getRenovatePipelineRunList()
			Expect(len(renovatePipelines)).Should(Equal(1))
			renovatePipeline := renovatePipelines[0]
			Expect(renovatePipeline.Spec.TaskRunTemplate.ServiceAccountName).To(Equal(getComponentServiceAccountKey(baseComponentKey).Name))
		})
	})

	Context("Test nudge failure handling", func() {
		var (
			baseComponentName = "base-component-failure"
			baseComponentKey  = types.NamespacedName{Namespace: UserNamespace, Name: baseComponentName}
			operator1Key      = types.NamespacedName{Namespace: UserNamespace, Name: "operator1-failure"}
			operator2Key      = types.NamespacedName{Namespace: UserNamespace, Name: "operator2-failure"}
		)

		_ = BeforeEach(func() {
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey:   baseComponentKey,
				containerImage: "quay.io/organization/repo:tag",
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done, so when we update BuildNudgesRef there won't be any new object
				},
			})
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey:   operator1Key,
				containerImage: "quay.io/organization/repo1:tag",
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done
				},
			})
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey:   operator2Key,
				containerImage: "quay.io/organization/repo2:tag",
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done
				},
			})
			baseComponent := applicationapi.Component{}
			err := k8sClient.Get(context.TODO(), baseComponentKey, &baseComponent)
			Expect(err).ToNot(HaveOccurred())
			baseComponent.Spec.BuildNudgesRef = []string{operator1Key.Name, operator2Key.Name}
			err = k8sClient.Update(context.TODO(), &baseComponent)
			Expect(err).ToNot(HaveOccurred())

			serviceAccountKey := getComponentServiceAccountKey(baseComponentKey)
			sa := waitServiceAccount(serviceAccountKey)
			sa.Secrets = []v1.ObjectReference{{Name: "dockerconfigjsonsecret"}}
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())
		})

		It("Test single failure results in retry", func() {
			failures = 1
			// mock function to produce error

			GenerateRenovateConfigForNudge = func(target updateTarget, buildResult *BuildResult, gitRepoAtShaAnnotation string) (RenovateConfig, error) {
				if failures == 0 {
					log.Info("components nudged")
					return RenovateConfig{}, nil
				}
				failures = failures - 1
				return RenovateConfig{}, fmt.Errorf("failure")
			}

			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, "")
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
				renovatePipelinesCreated := len(getRenovatePipelineRunList())

				return renovatePipelinesCreated == 1 && renovateConfigsCreated == 2
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
		})

		It("Test retries exceeded", func() {
			failures = 10
			// mock function to produce error
			GenerateRenovateConfigForNudge = func(target updateTarget, buildResult *BuildResult, gitRepoAtShaAnnotation string) (RenovateConfig, error) {
				if failures == 0 {
					log.Info("components nudged")
					return RenovateConfig{}, nil
				}
				failures = failures - 1
				return RenovateConfig{}, fmt.Errorf("failure")
			}

			createBuildPipelineRun("test-pipeline-1", UserNamespace, baseComponentName, "")
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
				renovatePipelinesCreated := len(getRenovatePipelineRunList())

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
			fileMatchesList := []string{"file1", "file2", "file3"}
			repositories := []renovateRepository{{BaseBranches: []string{"base_branch"}, Repository: "some_repository/something"}}
			buildImageRepository := "quay.io/testorg/testimage"
			builtImageTag := "a8dce08dbdf290e5d616a83672ad3afcb4b455ef"
			digest := "sha256:716be32f12f0dd31adbab1f57e9d0b87066e03de51c89dce8ffb397fbac92314"
			distributionRepositories := []string{"registry.redhat.com/some-product", "registry.redhat.com/other-product"}
			matchPackageNames := []string{buildImageRepository, distributionRepositories[0], distributionRepositories[1]}
			registryAliases := map[string]string{distributionRepositories[0]: buildImageRepository, distributionRepositories[1]: buildImageRepository}

			buildResult := BuildResult{
				BuiltImageRepository:     buildImageRepository,
				BuiltImageTag:            builtImageTag,
				Digest:                   digest,
				Component:                component,
				DistributionRepositories: distributionRepositories,
				FileMatches:              fileMatches,
			}

			automerge := true
			platformAutomerge := false
			commitMessagePrefix := "commit_prefix"
			commitMessageSuffix := "commit_suffix"
			imageBuiltFrom := "repo/sha"
			commitBody := fmt.Sprintf("Image created from '%s'\n\nSigned-off-by: %s", imageBuiltFrom, gitAuthor)
			automergeType := "pr"
			ignoreTests := true
			gitLabIgnoreApprovals := true
			automergeSchedule := []string{"* 22-23,0-4 * * *", "* * * * 0,6"}
			customLabels := []string{"custom1", "custom2"}
			labels := []string{DefaultRenovateLabel, "custom1", "custom2"}

			customRenovateOptions := CustomRenovateOptions{
				Automerge:             automerge,
				CommitMessagePrefix:   commitMessagePrefix,
				CommitMessageSuffix:   commitMessageSuffix,
				AutomergeType:         automergeType,
				PlatformAutomerge:     platformAutomerge,
				IgnoreTests:           ignoreTests,
				GitLabIgnoreApprovals: gitLabIgnoreApprovals,
				AutomergeSchedule:     automergeSchedule,
				Labels:                customLabels,
			}

			renovateTarget := updateTarget{
				ComponentName:                  "nudged component",
				ComponentCustomRenovateOptions: &customRenovateOptions,
				GitProvider:                    gitProvider,
				Username:                       gitUsername,
				GitAuthor:                      gitAuthor,
				Token:                          gitToken,
				Endpoint:                       gitEndpoint,
				Repositories:                   repositories,
				ImageRepositoryHost:            imageRepositoryHost,
				ImageRepositoryUsername:        imageRepositoryUsername,
			}

			resultConfig, err := generateRenovateConfigForNudge(renovateTarget, &buildResult, imageBuiltFrom)
			Expect(err).Should(Succeed())
			Expect(resultConfig.GitProvider).Should(Equal(gitProvider))
			Expect(resultConfig.Username).Should(Equal(gitUsername))
			Expect(resultConfig.GitAuthor).Should(Equal(gitAuthor))
			Expect(resultConfig.Repositories).Should(Equal(repositories))
			Expect(resultConfig.Endpoint).Should(Equal(gitEndpoint))
			Expect(resultConfig.CustomManagers[0].FileMatch).Should(Equal(fileMatchesList))
			Expect(resultConfig.CustomManagers[0].CurrentValueTemplate).Should(Equal(buildResult.BuiltImageTag))
			Expect(resultConfig.CustomManagers[0].DepNameTemplate).Should(Equal(buildImageRepository))
			Expect(resultConfig.PackageRules[1].GroupName).Should(Equal(fmt.Sprintf("Component Update %s", componentName)))
			Expect(resultConfig.PackageRules[1].BranchPrefix).Should(Equal("konflux/component-updates/"))
			Expect(resultConfig.PackageRules[1].BranchTopic).Should(Equal(componentName))
			Expect(resultConfig.PackageRules[1].CommitMessageTopic).Should(Equal(componentName))
			Expect(resultConfig.PackageRules[1].MatchPackageNames).Should(Equal(matchPackageNames))
			Expect(resultConfig.PackageRules[1].CommitMessagePrefix).Should(Equal(commitMessagePrefix))
			Expect(resultConfig.PackageRules[1].CommitMessageSuffix).Should(Equal(commitMessageSuffix))
			Expect(resultConfig.PackageRules[1].CommitBody).Should(Equal(commitBody))
			Expect(resultConfig.RegistryAliases).Should(Equal(registryAliases))
			Expect(resultConfig.Labels).Should(Equal(labels))
			Expect(resultConfig.Automerge).Should(Equal(automerge))
			Expect(resultConfig.AutomergeType).Should(Equal(automergeType))
			Expect(resultConfig.PlatformAutomerge).Should(Equal(platformAutomerge))
			Expect(resultConfig.IgnoreTests).Should(Equal(ignoreTests))
			Expect(resultConfig.GitLabIgnoreApprovals).Should(Equal(gitLabIgnoreApprovals))
			Expect(resultConfig.AutomergeSchedule).Should(Equal(automergeSchedule))

			// use fileMatch from config map instead of annotation
			fileMatchesList2 := []string{"ffile1", "ffile2", "ffile3"}
			customRenovateOptions.FileMatch = fileMatchesList2

			renovateTarget2 := updateTarget{
				ComponentName:                  "nudged component",
				ComponentCustomRenovateOptions: &customRenovateOptions,
				GitProvider:                    gitProvider,
				Username:                       gitUsername,
				GitAuthor:                      gitAuthor,
				Token:                          gitToken,
				Endpoint:                       gitEndpoint,
				Repositories:                   repositories,
				ImageRepositoryHost:            imageRepositoryHost,
				ImageRepositoryUsername:        imageRepositoryUsername,
			}

			resultConfig2, err := generateRenovateConfigForNudge(renovateTarget2, &buildResult, imageBuiltFrom)
			Expect(err).Should(Succeed())
			Expect(resultConfig2.CustomManagers[0].FileMatch).Should(Equal(fileMatchesList2))
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

func createBuildPipelineRun(name, namespace, component, imageBuiltFrom string) *tektonapi.PipelineRun {
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
	if imageBuiltFrom != "" {
		run.Annotations[gitRepoAtShaAnnotationName] = imageBuiltFrom
	}
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
		if strings.HasPrefix(configMap.ObjectMeta.Name, "renovate-") {
			log.Info(configMap.ObjectMeta.Name)
			renovateConfigMapList = append(renovateConfigMapList, configMap)
		}
	}
	return renovateConfigMapList
}

func getRenovatePipelineRunList() []tektonapi.PipelineRun {
	prList := &tektonapi.PipelineRunList{}
	renovatePipelinesList := []tektonapi.PipelineRun{}
	err := k8sClient.List(context.TODO(), prList, &client.ListOptions{Namespace: UserNamespace})
	Expect(err).ToNot(HaveOccurred())
	for _, pipelineRun := range prList.Items {
		if strings.HasPrefix(pipelineRun.ObjectMeta.Name, "renovate-pipeline") {
			log.Info(pipelineRun.ObjectMeta.Name)
			renovatePipelinesList = append(renovatePipelinesList, pipelineRun)
		}
	}
	return renovatePipelinesList
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
