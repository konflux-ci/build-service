/*
Copyright 2021-2026 Red Hat, Inc.

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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/h2non/gock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	"github.com/konflux-ci/build-service/pkg/boerrors"
	. "github.com/konflux-ci/build-service/pkg/common"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
)

const (
	githubAppPrivateKey = `-----BEGIN RSA PRIVATE KEY-----` + // notsecret
		`
MIIEogIBAAKCAQEAtSwZCtZ0Tnepuezo/TL9vhdP00fOedCpN3HsKjqz7zXTnqkq
foxemrDvRSg5n73sZhZMYX6NKY1FBLTE6OazvJQg0eXu7Bf5+5sg5ZZIthX1wbPU
wk18S5HsC1NTDGHVlQ2pQEuXmpxi/FAKgHaYbhx2J2SzD2gdKEOAufBZ14o8btF2
x64wi0VHX2JAmPXjodYQ2jYbH27lik5kS8wMDRKq0Kt3ABJkxULxUB4xuEbl2gWt
osDrhFTtDWEYtmtL0R5mLoK61FfZmZElvHELQObBcEpJu9F/Bi0mcjle/TuVc69e
tuVbmVg5Bn5iQKtw2hPEMqTLDVPbhhSAXhtJOwIDAQABAoIBAD+XEdce/MXJ9JXg
1MqCilOdZRRYoN1a4vomD2mnHx74OqX25IZ0iIQtVF5mxwsNo5sVeovB2pRaFH6Z
YIAK8c1gBMEHvru5krHAemR7Qlw/CvqJP0VP4y+3MS2senrfIBNoLx71KWpIN+ot
wfHjLo9/h+09yCfBOHK4dsdM2IvxTw9Md6ivipQC7rZmg0lpupNVRKwsx+VQoHCr
wzyPN14w+3tAJA2QoSdsqZLWkfY5YgxYmGuEG7L1A4Ynsjq6lEJyoclbZ6Gp+JML
G9MB0llXz9AV2k+BuNZWQO4cBPeKYtiQ1O5iYohghjSgPIx0iFa/GAvIrZscF2cf
Y43bN/ECgYEA3pKZ55L+oZIym0P2t9tqUaFhFewLdc54WhoMBrjKDQ7PL5n/avG5
Tac1RKA7AoknLYkqWilwRAzSBzNDjYsFVbfxRA10LagP61WhgGAPtEsDM3gbwuNw
HayYowutQ6422Ri4+ujWSeMMSrvx7RcHMPpr3hDSm8ihxbqsWkDNGQMCgYEA0GG/
cdtidDZ0aPwIqBdx6DHwKraYMt1fIzkw1qG59X7jc/Rym2aPUzDob99q8BieBRB3
6djzn+X97z40/87DRFACj/YQPkGvumSdRUtP88b0x87UhbHoOfEIkDeVYyxkbfl0
Mo6H9tlw+QSTaU13AzIBaWOB4nsQaKAM9isHrWkCgYAO0F8iBKyiAGMR5oIjVp1K
9ZzKor1Yh/eGt7kZMW9xUw0DNBLGAXS98GUhPjDvSEWtSDXjbmKkhN3t0MGsSBaA
0A9k4ihbaZY1qatoKfyhmWSLJnFilVS/BN/b6kkL+ip4ZKbbPGgW3t/QkZXWm/PE
lMZdL211JPNvf689CpccFQKBgGXJGUZ4LuMtJjeRxHi22wDcQ7/ZaQaPc0U1TlHI
tZjg3iFpqgGWWzP7k83xh763h5hZrvke7AGSyjLuY90AFglsO5QuUUjXtQqK0vdi
Di+5Yx+mO9ECUbjbr58iR2ol6Ph+/O8lB+zf0XsRbR/moteAuYfM/0itbBpu82Xb
JujhAoGAVdNMYdamHOJlkjYjYJmWOHPIFBMTDYk8pDATLOlqthV+fzlD2SlF0wxm
OlxbwEr3BSQlE3GFPHe+J3JV3BbJFsVkM8KdMUpQCl+aD/3nWaGBYHz4yvJc0i9h
duAFIZgXeivAZQAqys4JanG81cg4/d4urI3Qk9stlhB5CNCJR4k=
-----END RSA PRIVATE KEY-----`
)

var _ = Describe("Component build controller new model", func() {

	const (
		pacHost       = "pac-host"
		pacWebhookUrl = "https://" + pacHost
	)

	var (
		pacRouteKey  = types.NamespacedName{Name: pipelinesAsCodeRouteName, Namespace: pipelinesAsCodeNamespace}
		pacSecretKey = types.NamespacedName{Name: PipelinesAsCodeGitHubAppSecretName, Namespace: BuildServiceNamespaceName}
	)

	BeforeEach(func() {
		createNamespace(BuildServiceNamespaceName)
	})

	// Tests for build pipeline Service Account and Role Binding management
	Context("Build pipeline Service Account", func() {
		var (
			namespace           = "sa-test"
			component1Key       = types.NamespacedName{Name: "component-sa-1", Namespace: namespace}
			component2Key       = types.NamespacedName{Name: "component-sa-2", Namespace: namespace}
			buildRoleBindingKey = types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			// Clean up individually since these tests verify SA and role binding behavior
			deleteServiceAccount(getComponentServiceAccountKey(component1Key))
			deleteServiceAccount(getComponentServiceAccountKey(component2Key))
			deleteComponent(component1Key)
			deleteComponent(component2Key)
			deleteRoleBinding(buildRoleBindingKey)
		})

		It("should create build pipeline dedicated service account and role binding", func() {
			createComponent(getSampleComponentData(component1Key))

			component1SAKey := getComponentServiceAccountKey(component1Key)
			waitServiceAccount(component1SAKey)
			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(1))
		})

		It("should create build pipeline dedicated service account for each component and common role binding", func() {
			createComponent(getSampleComponentData(component1Key))

			component1SAKey := getComponentServiceAccountKey(component1Key)
			waitServiceAccount(component1SAKey)
			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(1))

			createComponent(getSampleComponentData(component2Key))

			component2SAKey := getComponentServiceAccountKey(component2Key)
			waitServiceAccount(component2SAKey)
			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(2))
		})

		It("should remove build pipeline dedicated service account for each component and common role binding when the last service account removed", func() {
			createComponent(getSampleComponentData(component1Key))

			component1SAKey := getComponentServiceAccountKey(component1Key)
			component1SA := waitServiceAccount(component1SAKey)
			Expect(component1SA.OwnerReferences).To(HaveLen(1))
			Expect(component1SA.OwnerReferences[0].Name).To(Equal(component1Key.Name))
			Expect(component1SA.OwnerReferences[0].Kind).To(Equal("Component"))

			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))

			createComponent(getSampleComponentData(component2Key))

			component2SAKey := getComponentServiceAccountKey(component2Key)
			component2SA := waitServiceAccount(component2SAKey)
			Expect(component2SA.OwnerReferences).To(HaveLen(1))
			Expect(component2SA.OwnerReferences[0].Name).To(Equal(component2Key.Name))
			Expect(component2SA.OwnerReferences[0].Kind).To(Equal("Component"))

			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(2))

			deleteComponent(component1Key)

			roleBinding = waitServiceAccountNotInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(1))
			Expect(roleBinding.Subjects[0].Kind).To(Equal("ServiceAccount"))
			Expect(roleBinding.Subjects[0].Name).To(Equal(component2SAKey.Name))

			deleteComponent(component2Key)

			waitRoleBindingGone(buildRoleBindingKey)
		})

		It("should restore build pipeline dedicated service account on reconcile", func() {
			createComponent(getSampleComponentData(component1Key))

			// Wait for version to appear in status
			waitForComponentStatusVersions(component1Key, 1)

			component1SAKey := getComponentServiceAccountKey(component1Key)
			waitServiceAccount(component1SAKey)
			waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)

			deleteServiceAccount(component1SAKey)

			// trigger a reconcile
			component1 := getComponent(component1Key)
			component1.Spec.ContainerImage = "quay.io/test/image:reconcile"
			Expect(k8sClient.Update(ctx, component1)).To(Succeed())

			waitServiceAccount(component1SAKey)
			waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
		})

		It("should restore build pipelines role binding on reconcile", func() {
			createComponent(getSampleComponentData(component1Key))

			// Wait for version to appear in status
			waitForComponentStatusVersions(component1Key, 1)

			component1SAKey := getComponentServiceAccountKey(component1Key)
			waitServiceAccount(component1SAKey)
			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(1))

			createComponent(getSampleComponentData(component2Key))

			// Wait for version to appear in status
			waitForComponentStatusVersions(component2Key, 1)

			component2SAKey := getComponentServiceAccountKey(component2Key)
			waitServiceAccount(component2SAKey)
			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(2))

			deleteRoleBinding(buildRoleBindingKey)

			// trigger a reconcile for component 1
			component1Get := getComponent(component1Key)
			component1Get.Spec.ContainerImage = "quay.io/test/image1:reconcile"
			Expect(k8sClient.Update(ctx, component1Get)).To(Succeed())
			// trigger a reconcile for component 2
			component2Get := getComponent(component2Key)
			component2Get.Spec.ContainerImage = "quay.io/test/image2:reconcile"
			Expect(k8sClient.Update(ctx, component2Get)).To(Succeed())

			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.Subjects).To(HaveLen(2))
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
		})

		It("should create PaC PR with build pipeline service account", func() {
			prCreatedCount := 0
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					buildPipelineData := &tektonapi.PipelineRun{}
					Expect(yaml.Unmarshal(file.Content, buildPipelineData)).To(Succeed())
					Expect(buildPipelineData.Spec.TaskRunTemplate.ServiceAccountName).To(Equal(getComponentServiceAccountKey(component1Key).Name))
				}

				prCreatedCount++
				return "merge-url", nil
			}

			component := getComponentData(componentConfig{
				componentKey: component1Key,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						AllVersions: true,
					},
				},
			})
			createComponent(component)

			Eventually(func() bool {
				return prCreatedCount == 1
			}, timeout, interval).Should(BeTrue())
		})
	})

	// These tests verifies component validation errors (version fields, required spec fields, missing PaC secret)
	// are properly reported in component status
	Context("Component validation errors", func() {
		var (
			namespace    = "validation-ns"
			componentKey = types.NamespacedName{Name: "test-component", Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
		})

		_ = AfterEach(func() {
			deleteComponent(componentKey)
		})

		It("should set error status when version name is empty", func() {
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "", Revision: "main"},
				},
			})
			createComponent(component)

			component = waitForComponentStatusMessage(componentKey, false)

			Expect(component.Status.Message).To(ContainSubstring("validation failed"))
			Expect(component.Status.Message).To(ContainSubstring("name is required"))
		})

		It("should set error status when version revision is empty", func() {
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1.0", Revision: ""},
				},
			})
			createComponent(component)

			component = waitForComponentStatusMessage(componentKey, false)

			Expect(component.Status.Message).To(ContainSubstring("validation failed"))
			Expect(component.Status.Message).To(ContainSubstring("revision is required"))
		})

		It("should set error status for duplicate sanitized version names", func() {
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1.0", Revision: "main"},    // becomes "v1-0" after sanitization
					{Name: "v1_0", Revision: "develop"}, // becomes "v1-0" after sanitization
				},
			})
			createComponent(component)

			component = waitForComponentStatusMessage(componentKey, false)

			Expect(component.Status.Message).To(ContainSubstring("validation failed"))
			Expect(component.Status.Message).To(ContainSubstring("conflicts with version"))
		})

		It("should set error status when git source URL is missing", func() {
			component := getSampleComponentData(componentKey)
			component.Spec.Source.GitURL = ""
			createComponent(component)

			component = waitForComponentStatusMessage(componentKey, false)

			Expect(component.Status.Message).To(ContainSubstring("Nothing to do for component without git source"))
		})

		It("should set error status when container image is missing", func() {
			component := getSampleComponentData(componentKey)
			component.Spec.ContainerImage = ""
			createComponent(component)

			component = waitForComponentStatusMessage(componentKey, false)

			Expect(component.Status.Message).To(Equal(waitForContainerImageMessage))
		})

		It("should set error status when PaC secret is missing", func() {
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				gitURL:       "https://gitlab.com/test-org/test-repo",
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
				},
			})
			createComponent(component)

			component = waitForComponentStatusMessage(componentKey, false)

			Expect(component.Status.Message).To(ContainSubstring("Pipelines as Code secret does not exist"))
		})
	})

	// Tests for pipeline configuration validation errors
	// These tests verify pipeline validation happens when CreateConfiguration action is present
	Context("Pipeline configuration validation errors", func() {
		var (
			namespace    = "pipeline-validation-ns"
			componentKey = types.NamespacedName{Name: "test-component", Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
		})

		It("should set error status when PullAndPush is used with Pull/Push", func() {
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						AllVersions: true,
					},
				},
				defaultPipeline: compapiv1alpha1.ComponentBuildPipeline{
					PullAndPush: compapiv1alpha1.PipelineDefinition{
						PipelineRefName: "docker-build",
					},
					Pull: compapiv1alpha1.PipelineDefinition{
						PipelineRefName: "docker-build",
					},
				},
			})
			createComponent(component)

			component = waitForComponentStatusMessage(componentKey, false)

			Expect(component.Status.Message).To(ContainSubstring("validation failed"))
			Expect(component.Status.Message).To(ContainSubstring("cannot specify pull-and-push together with pull or push"))
		})

		It("should set error status for missing PipelineRefGit required fields", func() {
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						AllVersions: true,
					},
				},
				defaultPipeline: compapiv1alpha1.ComponentBuildPipeline{
					Pull: compapiv1alpha1.PipelineDefinition{
						PipelineRefGit: compapiv1alpha1.PipelineRefGit{
							Url: "https://github.com/test/pipelines",
							// Missing PathInRepo and Revision
						},
					},
				},
			})
			createComponent(component)

			component = waitForComponentStatusMessage(componentKey, false)

			Expect(component.Status.Message).To(ContainSubstring("validation failed"))
			Expect(component.Status.Message).To(ContainSubstring("pathInRepo is required"))
			Expect(component.Status.Message).To(ContainSubstring("revision is required"))
		})
	})

	// Tests for Repository custom params (requires namespace with workspace label)
	Context("Repository custom params", func() {
		var (
			namespace    = "repo-custom-params-ns"
			componentKey = types.NamespacedName{Name: "test-component-params", Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			contextNamespace := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, contextNamespace)).Should(Succeed())
			contextNamespace.SetLabels(map[string]string{
				appstudioWorkspaceNameLabel: "build",
			})
			Expect(k8sClient.Update(ctx, contextNamespace)).Should(Succeed())

			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
		})

		It("should set custom params in Repository CR when namespace has workspace label", func() {
			createComponent(getSampleComponentData(componentKey))

			// Wait for version to appear in status
			component := waitForComponentStatusVersions(componentKey, 1)

			// Verify Repository CR has custom params
			expectedRepoName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
			Expect(err).NotTo(HaveOccurred())
			pacRepoKey := types.NamespacedName{Name: expectedRepoName, Namespace: namespace}
			pacRepo := waitPaCRepositoryCreated(pacRepoKey)
			Expect(pacRepo.Spec.Params).ShouldNot(BeNil())
			Expect(*pacRepo.Spec.Params).Should(ContainElement(And(
				HaveField("Name", "appstudio_workspace"),
				HaveField("Value", "build"),
			)))
		})
	})

	// Tests for repository settings updates
	// These tests verify PaC repository is updated when settings change
	Context("Repository settings updates", func() {
		var (
			namespace    = "repo-settings-ns"
			componentKey = types.NamespacedName{Name: "test-component", Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
		})

		It("should create PaC repository with initial settings and update when settings change", func() {
			versionName := "v1"

			// Create component with initial settings
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
				repositorySettings: compapiv1alpha1.RepositorySettings{
					CommentStrategy:          "disable_all",
					GithubAppTokenScopeRepos: []string{"owner/repo1", "owner/repo2"},
				},
			})
			createComponent(component)

			// Wait for initial onboarding (version in status means reconcile finished)
			component = waitForComponentStatusVersions(componentKey, 1)

			// Verify component status fields
			verifyComponentVersionStatus(component.Status.Versions[0], versionName, "main", "succeeded", "", "")

			expectedRepoName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
			Expect(err).NotTo(HaveOccurred())
			Expect(component.Status.PacRepository).To(Equal(expectedRepoName))
			Expect(component.Status.RepositorySettings.CommentStrategy).To(Equal("disable_all"))
			Expect(component.Status.RepositorySettings.GithubAppTokenScopeRepos).To(Equal([]string{"owner/repo1", "owner/repo2"}))

			// Verify Repository CR fields
			repositoryKey := types.NamespacedName{Namespace: componentKey.Namespace, Name: expectedRepoName}
			repository := waitPaCRepositoryCreated(repositoryKey)

			expectedURL := strings.TrimSuffix(strings.TrimSuffix(component.Spec.Source.GitURL, "/"), ".git")
			Expect(repository.Spec.URL).To(Equal(expectedURL))
			Expect(repository.Spec.Settings.Github.CommentStrategy).To(Equal("disable_all"))
			Expect(repository.Spec.Settings.Gitlab.CommentStrategy).To(Equal("disable_all"))
			Expect(repository.Spec.Settings.GithubAppTokenScopeRepos).To(Equal([]string{"owner/repo1", "owner/repo2"}))

			// Update repository settings (change from disable_all to empty string)
			component.Spec.RepositorySettings.CommentStrategy = ""
			component.Spec.RepositorySettings.GithubAppTokenScopeRepos = []string{"owner/repo3"}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for settings to be updated in status
			Eventually(func() bool {
				component = getComponent(componentKey)
				return component.Status.RepositorySettings.CommentStrategy == "" &&
					len(component.Status.RepositorySettings.GithubAppTokenScopeRepos) == 1
			}, timeout, interval).Should(BeTrue())

			// Verify updated component status fields
			Expect(component.Status.RepositorySettings.CommentStrategy).To(Equal(""))
			Expect(component.Status.RepositorySettings.GithubAppTokenScopeRepos).To(Equal([]string{"owner/repo3"}))

			// Verify updated Repository CR fields
			repository = waitPaCRepositoryCreated(repositoryKey)
			Expect(repository.Spec.Settings.Github.CommentStrategy).To(Equal(""))
			Expect(repository.Spec.Settings.Gitlab.CommentStrategy).To(Equal(""))
			Expect(repository.Spec.Settings.GithubAppTokenScopeRepos).To(Equal([]string{"owner/repo3"}))
		})

		It("should not update status when GithubAppTokenScopeRepos order changes but content is same", func() {
			versionName := "v1"

			// Create component with initial repo list
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
				repositorySettings: compapiv1alpha1.RepositorySettings{
					GithubAppTokenScopeRepos: []string{"owner/repo1", "owner/repo2"},
				},
			})
			createComponent(component)

			// Wait for initial onboarding
			component = waitForComponentStatusVersions(componentKey, 1)

			// Verify initial status
			Expect(component.Status.RepositorySettings.GithubAppTokenScopeRepos).To(Equal([]string{"owner/repo1", "owner/repo2"}))

			// Update with same repos but different order
			component.Spec.RepositorySettings.GithubAppTokenScopeRepos = []string{"owner/repo2", "owner/repo1"}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Status should NOT be updated since the repo sets are equivalent (order doesn't matter)
			Consistently(func() []string {
				component = getComponent(componentKey)
				// Status should still have original order since content didn't change
				return component.Status.RepositorySettings.GithubAppTokenScopeRepos
			}, "3s", interval).Should(Equal([]string{"owner/repo1", "owner/repo2"}))
		})
	})

	// Tests for invalid version handling in actions
	// These tests verify that invalid/nonexistent versions are removed from actions
	Context("Invalid version handling in actions", func() {
		var (
			namespace    = "invalid-version-ns"
			componentKey = types.NamespacedName{Name: "test-component", Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
		})

		It("should remove invalid versions from CreateConfiguration action", func() {
			versionName := "v1"

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						Versions: []string{versionName, "nonexistent"},
					},
				},
			})
			createComponent(component)

			// Wait for action to be completely cleared (invalid version removed and valid version processed)
			component = waitForComponentSpecActionEmpty(componentKey, "create-pr", "versions")

			// Verify v1 is onboarded in status
			Expect(len(component.Status.Versions)).To(Equal(1))
			verifyComponentVersionStatus(component.Status.Versions[0], versionName, "main", "succeeded", "*", "")
		})

		It("should remove invalid versions from TriggerBuilds action", func() {
			versionName := "v1"

			// First onboard v1
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
			})
			createComponent(component)

			// Wait for v1 to be onboarded
			component = waitForComponentStatusVersions(componentKey, 1)

			// Now add trigger build action with invalid version
			component.Spec.Actions.TriggerBuilds = []string{versionName, "nonexistent"}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for action to be completely cleared (invalid version removed and valid version triggered)
			component = waitForComponentSpecActionEmpty(componentKey, "trigger", "builds")

			// Verify v1 is still onboarded in status
			Expect(len(component.Status.Versions)).To(Equal(1))
			verifyComponentVersionStatus(component.Status.Versions[0], versionName, "main", "succeeded", "", "")
		})
	})

	// Tests for version onboarding without configuration
	// These tests verify versions are onboarded to status without creating PR
	Context("Version onboarding without configuration", func() {
		var (
			namespace           = "onboard-no-config-ns"
			componentKey        = types.NamespacedName{Name: "test-component", Namespace: namespace}
			component1Key       = types.NamespacedName{Name: "component-1", Namespace: namespace}
			component2Key       = types.NamespacedName{Name: "component-2", Namespace: namespace}
			buildRoleBindingKey = types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
			deleteComponentAndOwnedResources(component1Key)
			deleteComponentAndOwnedResources(component2Key)
		})

		It("should onboard versions without creating PR when no CreateConfiguration action", func() {
			// Verify SetupPaCWebhookFunc is NOT called when using GitHub App
			SetupPaCWebhookFunc = func(string, string, string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
					{Name: "v2", Revision: "develop"},
				},
			})
			createComponent(component)

			// Wait for versions to appear in status
			component = waitForComponentStatusVersions(componentKey, 2)

			// Verify both versions are onboarded with correct status fields
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			Expect(versionMap).To(HaveKey("v1"))
			verifyComponentVersionStatus(versionMap["v1"], "v1", "main", "succeeded", "", "")
			Expect(versionMap).To(HaveKey("v2"))
			verifyComponentVersionStatus(versionMap["v2"], "v2", "develop", "succeeded", "", "")

			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			validatePaCRepository(component, "", "")
		})

		It("should onboard new version when added to existing component", func() {
			// Create component with one version
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
				},
			})
			createComponent(component)

			// Wait for v1 to be onboarded
			component = waitForComponentStatusVersions(componentKey, 1)

			// Add v2
			component.Spec.Source.Versions = append(component.Spec.Source.Versions,
				compapiv1alpha1.ComponentVersion{Name: "v2", Revision: "develop"})
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for v2 to be onboarded
			component = waitForComponentStatusVersions(componentKey, 2)

			// Verify both versions are in status with correct status fields
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			Expect(versionMap).To(HaveKey("v1"))
			verifyComponentVersionStatus(versionMap["v1"], "v1", "main", "succeeded", "", "")
			Expect(versionMap).To(HaveKey("v2"))
			verifyComponentVersionStatus(versionMap["v2"], "v2", "develop", "succeeded", "", "")

			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			// Verify service account still exists
			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			validatePaCRepository(component, "", "")
		})

		It("should remove version while others remain", func() {
			version1Name := "v1"
			version2Name := "v2"

			// Create component with two versions
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
			})
			createComponent(component)

			// Wait for both to be onboarded
			component = waitForComponentStatusVersions(componentKey, 2)

			// Remove v2
			component.Spec.Source.Versions = []compapiv1alpha1.ComponentVersion{
				{Name: version1Name, Revision: "main"},
			}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for v2 to be removed from status
			component = waitForComponentStatusVersions(componentKey, 1)

			// Verify only v1 remains with all correct status fields
			verifyComponentVersionStatus(component.Status.Versions[0], version1Name, "main", "succeeded", "", "")

			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			// Verify service account still exists (not removed when offboarding version)
			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			validatePaCRepository(component, "", "")
		})

		It("should reuse PaC repository for multiple components with same git URL", func() {
			versionName := "v1"

			sharedGitURL := "https://github.com/test-org/shared-repo"

			// Create default component with default git URL
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
			})
			component = createComponent(component)

			// Wait for default component to be onboarded
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 1 && component.Status.PacRepository != ""
			}, timeout, interval).Should(BeTrue())

			// Verify Repository CR for component has correct URL and no GitProvider (GitHub App)
			validatePaCRepository(component, "", "")

			// Create first component with shared git URL
			component1 := getComponentData(componentConfig{
				componentKey: component1Key,
				gitURL:       sharedGitURL,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
			})
			createComponent(component1)

			// Wait for first component to be onboarded
			Eventually(func() bool {
				component1 = getComponent(component1Key)
				return len(component1.Status.Versions) == 1 && component1.Status.PacRepository != ""
			}, timeout, interval).Should(BeTrue())

			// Verify the shared repository is different from default component's repository
			Expect(component.Status.PacRepository).NotTo(Equal(component1.Status.PacRepository))

			// Verify Repository CR for component1 has correct URL and no GitProvider (GitHub App)
			sharedPacRepository := validatePaCRepository(component1, "", "")
			Expect(sharedPacRepository.OwnerReferences).To(HaveLen(1))
			Expect(sharedPacRepository.OwnerReferences[0].Name).To(Equal(component1Key.Name))
			Expect(sharedPacRepository.OwnerReferences[0].Kind).To(Equal("Component"))

			// Create second component with same shared git URL
			component2 := getComponentData(componentConfig{
				componentKey: component2Key,
				gitURL:       sharedGitURL,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
			})
			createComponent(component2)

			// Wait for second component to be onboarded
			Eventually(func() bool {
				component2 = getComponent(component2Key)
				return len(component2.Status.Versions) == 1 && component2.Status.PacRepository != ""
			}, timeout, interval).Should(BeTrue())

			// Both components with shared URL should reference the same PaC repository
			Expect(component1.Status.PacRepository).To(Equal(component2.Status.PacRepository))

			// Verify Repository CR for component2 has correct URL and no GitProvider (GitHub App)
			sharedPacRepository = validatePaCRepository(component2, "", "")

			// Verify shared Repository has both component1 and component2 as owners
			Expect(sharedPacRepository.OwnerReferences).To(HaveLen(2))
			hasComponent1 := false
			hasComponent2 := false
			for _, owner := range sharedPacRepository.OwnerReferences {
				if owner.Name == component1Key.Name && owner.Kind == "Component" {
					hasComponent1 = true
				}
				if owner.Name == component2Key.Name && owner.Kind == "Component" {
					hasComponent2 = true
				}
			}
			Expect(hasComponent1).To(BeTrue())
			Expect(hasComponent2).To(BeTrue())

			// Verify there are exactly 2 Repository CRs in the namespace
			var pacRepositoriesList pacv1alpha1.RepositoryList
			Expect(k8sClient.List(ctx, &pacRepositoriesList, &client.ListOptions{Namespace: namespace})).To(Succeed())
			Expect(pacRepositoriesList.Items).To(HaveLen(2))

			// Verify finalizer was added to all components
			component = getComponent(componentKey)
			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))
			component1 = getComponent(component1Key)
			Expect(component1.Finalizers).To(ContainElement(PaCProvisionFinalizer))
			component2 = getComponent(component2Key)
			Expect(component2.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			// Verify all three service accounts were created
			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)
			sa1Key := getComponentServiceAccountKey(component1Key)
			waitServiceAccount(sa1Key)
			sa2Key := getComponentServiceAccountKey(component2Key)
			waitServiceAccount(sa2Key)

			// Verify role binding exists and all three service accounts are in it
			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
			waitServiceAccountInRoleBinding(buildRoleBindingKey, sa1Key.Name)
			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, sa2Key.Name)
			Expect(roleBinding.Subjects).To(HaveLen(3))
		})

		It("should handle component with no versions defined", func() {
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions:     []compapiv1alpha1.ComponentVersion{},
			})
			component = createComponent(component)

			// Component should be created but with empty status.Versions
			Consistently(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 0
			}, "2s", interval).Should(BeTrue())
		})

		It("should handle version names with special characters", func() {
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1.0-beta+build.123", Revision: "main"},
				},
			})
			createComponent(component)

			// Version should be onboarded (after sanitization)
			component = waitForComponentStatusVersions(componentKey, 1)
			// Original name should be preserved in status
			verifyComponentVersionStatus(component.Status.Versions[0], "v1.0-beta+build.123", "main", "succeeded", "", "")

			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			validatePaCRepository(component, "", "")
		})

		It("should handle updating version revision", func() {
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
				},
			})
			component = createComponent(component)

			// Wait for initial onboarding
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 1 && component.Status.Versions[0].Revision == "main"
			}, timeout, interval).Should(BeTrue())

			// Update revision - changing spec.revision triggers reconcile but doesn't update status
			// Need to explicitly trigger CreateConfiguration to apply the revision change
			component.Spec.Source.Versions[0].Revision = "develop"
			component.Spec.Actions.CreateConfiguration.Version = "v1"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// CreateConfiguration should be cleared after processing
			component = waitForComponentSpecActionEmpty(componentKey, "create-pr", "version")

			// Status should now reflect the new revision
			component = getComponent(componentKey)
			Expect(component.Status.Versions).To(HaveLen(1))
			verifyComponentVersionStatus(component.Status.Versions[0], "v1", "develop", "succeeded", "*", "")

			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			validatePaCRepository(component, "", "")
		})

		It("should clear validation errors when spec is fixed", func() {
			// Create component with invalid spec
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "", Revision: "main"},
				},
			})
			createComponent(component)

			// Wait for error to be set
			component = waitForComponentStatusMessage(componentKey, false)

			// Fix the spec
			component.Spec.Source.Versions[0].Name = "v1"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Error should be cleared
			component = waitForComponentStatusMessage(componentKey, true)

			// Version should be onboarded
			Expect(component.Status.Versions).To(HaveLen(1))
			verifyComponentVersionStatus(component.Status.Versions[0], "v1", "main", "succeeded", "", "")

			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			validatePaCRepository(component, "", "")
		})

		It("should set permanent error when PAC secret is missing", func() {
			// Delete the PAC secret to simulate missing configuration
			deleteSecret(pacSecretKey)

			// Create component with one version
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
				},
			})
			createComponent(component)

			// Wait for permanent error to be set in status.message
			component = waitForComponentStatusMessage(componentKey, false)

			// Verify the error message contains information about PAC secret not found
			Expect(component.Status.Message).To(ContainSubstring("Pipelines as Code secret does not exist"))

			// Verify version is not onboarded (no status.Versions entries)
			Expect(component.Status.Versions).To(HaveLen(0))

			deleteComponent(componentKey)
		})
	})

	// Tests for version offboarding
	// These tests verify that versions removed from spec are offboarded (both with and without SkipOffboardingPr)
	Context("Version offboarding", func() {
		var (
			namespace           = "offboard-ns"
			componentKey        = types.NamespacedName{Name: "test-component", Namespace: namespace}
			buildRoleBindingKey = types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
		})

		It("should offboard version when removed from spec (skipping PR)", func() {
			version1Name := "v1"
			version2Name := "v2"

			// Create component with two versions (SkipOffboardingPr=true)
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
				skipOffboardingPr: true,
			})
			createComponent(component)

			// Wait for both versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 2)

			// Remove v2 from spec
			component.Spec.Source.Versions = []compapiv1alpha1.ComponentVersion{
				{Name: version1Name, Revision: "main"},
			}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for v2 to be removed from status
			Eventually(func() bool {
				component = getComponent(componentKey)
				if len(component.Status.Versions) != 1 {
					return false
				}
				return component.Status.Versions[0].Name == version1Name
			}, timeout, interval).Should(BeTrue())

			// Verify v1 remains with correct status fields
			verifyComponentVersionStatus(component.Status.Versions[0], version1Name, "main", "succeeded", "", "")

			// Verify service account still exists (not removed when offboarding version)
			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
		})

		It("should create purge PR when offboarding version without SkipOffboardingPr", func() {
			version1Name := "v1"
			version2Name := "v2"

			// Verify DeletePaCWebhookFunc is NOT called when using GitHub App (only offboarding version, not component)
			DeletePaCWebhookFunc = func(string, string) error {
				defer GinkgoRecover()
				Fail("Should not try to delete webhook if GitHub application is used")
				return nil
			}

			isPurgePRCreated := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				defer GinkgoRecover()

				// Only validate v2 on first call (during test), allow v1 on subsequent calls (during AfterEach cleanup)
				if !isPurgePRCreated {
					// Verify MergeRequestData fields using helper
					expectedGitURL := SampleRepoLink + "-" + componentKey.Name
					commitMessage := "git-app-name purge " + componentKey.Name + ":" + version2Name
					title := "git-app-name purge " + componentKey.Name + ":" + version2Name
					branchName := "konflux-purge-" + componentKey.Name + "-" + version2Name
					baseBranch := "develop"
					verifyMergeRequestData(repoUrl, d, expectedGitURL, commitMessage, title, branchName, baseBranch)
				}

				// Verify purge PR data contains files to delete
				Expect(len(d.Files)).To(Equal(2), "Should delete both pull and push pipeline files")
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue(), "File should be in .tekton directory")
					// Files should be empty for deletion
					Expect(file.Content).To(BeNil())
				}

				isPurgePRCreated = true
				return UndoPacMergeRequestURL, nil
			}

			// Create component with two versions without SkipOffboardingPr
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
				// skipOffboardingPr is false by default
			})
			createComponent(component)

			// Wait for both versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 2)

			// Remove v2 from spec (trigger offboarding with purge PR)
			component.Spec.Source.Versions = []compapiv1alpha1.ComponentVersion{
				{Name: version1Name, Revision: "main"},
			}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for purge PR to be created
			Eventually(func() bool {
				return isPurgePRCreated
			}, timeout, interval).Should(BeTrue())

			// Wait for v2 to be removed from status
			Eventually(func() bool {
				component = getComponent(componentKey)
				if len(component.Status.Versions) != 1 {
					return false
				}
				return component.Status.Versions[0].Name == version1Name
			}, timeout, interval).Should(BeTrue())

			// Verify v1 remains with correct status fields
			verifyComponentVersionStatus(component.Status.Versions[0], version1Name, "main", "succeeded", "", "")

			// Verify service account still exists (not removed when offboarding version)
			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
		})

		It("should handle partial success when one version offboards successfully and another fails", func() {
			// Use version names that ensure processing order: alphabetically sorted
			version1Name := "v1" // stays in spec
			version2Name := "v2" // will be removed and offboard successfully
			version3Name := "v3" // will be removed but fail offboarding

			v2PurgePRCreated := false
			v3PurgePRAttempted := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				defer GinkgoRecover()

				// Determine which version this is for based on branch name
				if strings.Contains(d.BranchName, version2Name) {
					// v2 version succeeds
					v2PurgePRCreated = true
					expectedGitURL := SampleRepoLink + "-" + componentKey.Name
					Expect(repoUrl).To(Equal(expectedGitURL))
					Expect(d.CommitMessage).ToNot(BeEmpty())
					Expect(d.BaseBranchName).To(Equal("branch-v2"))
					Expect(len(d.Files)).To(Equal(2), "Should delete both pull and push pipeline files")
					return UndoPacMergeRequestURL, nil
				} else if strings.Contains(d.BranchName, version3Name) {
					// v3 version fails with insufficient scope error
					v3PurgePRAttempted = true
					return "", boerrors.NewBuildOpError(boerrors.EGitLabTokenInsufficientScope, fmt.Errorf("403 Forbidden"))
				} else if strings.Contains(d.BranchName, version1Name) {
					// v1 version can be called during AfterEach cleanup, just succeed
					return UndoPacMergeRequestURL, nil
				}

				Fail("Unexpected version in offboarding: " + d.BranchName)
				return "", nil
			}

			// Create component with three versions
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "branch-v1"},
					{Name: version2Name, Revision: "branch-v2"},
					{Name: version3Name, Revision: "branch-v3"},
				},
			})
			createComponent(component)

			// Wait for all three versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 3)

			// Remove v2 and v3 versions from spec (trigger offboarding for both)
			component.Spec.Source.Versions = []compapiv1alpha1.ComponentVersion{
				{Name: version1Name, Revision: "branch-v1"},
			}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for both offboarding attempts and v2 version to be removed from status
			Eventually(func() bool {
				component = getComponent(componentKey)
				if !v2PurgePRCreated || !v3PurgePRAttempted {
					return false
				}
				// Should have 2 versions: v1 version (still in spec) and v3 version (failed offboarding)
				if len(component.Status.Versions) != 2 {
					return false
				}
				// Check if v3 version has error message set
				for _, v := range component.Status.Versions {
					if v.Name == version3Name && v.Message != "" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			Expect(v2PurgePRCreated).To(BeTrue(), "v2 version offboarding should have been attempted")
			Expect(v3PurgePRAttempted).To(BeTrue(), "v3 version offboarding should have been attempted")

			// Verify version states
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			// v1 version should remain with succeeded status (still in spec)
			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "branch-v1", "succeeded", "", "")

			// v2 version should be completely removed from status (successfully offboarded)
			Expect(versionMap).NotTo(HaveKey(version2Name))

			// v3 version should remain in status with error message (failed offboarding)
			Expect(versionMap).To(HaveKey(version3Name))
			verifyComponentVersionStatus(versionMap[version3Name], version3Name, "branch-v3", "succeeded", "", "91: GitLab access token does not have enough scope")

			// Verify service account still exists
			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
		})
	})

	// Tests for CreateConfiguration action with onboarding
	// These tests verify PR creation with different pipeline configurations during component onboarding
	Context("CreateConfiguration action - onboarding with PR creation and pipeline verification", func() {
		var (
			namespace           = "create-config-ns"
			componentKey        = types.NamespacedName{Name: "test-component", Namespace: namespace}
			buildRoleBindingKey = types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
		})

		It("should create PR with PipelineSpecFromBundle from default config (no version/defaultPipeline specified) using AllVersions", func() {
			version1Name := "v1"
			version2Name := "v2"
			v1PrUrl := "https://githost.com/mr/1111"
			v2PrUrl := "https://githost.com/mr/2222"

			// Verify SetupPaCWebhookFunc is NOT called when using GitHub App
			SetupPaCWebhookFunc = func(string, string, string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			prCreatedCount := 0
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				// Determine which version this PR is for
				var versionName, baseBranch string
				if d.BranchName == "konflux-"+componentKey.Name+"-"+version1Name {
					versionName = version1Name
					baseBranch = "main"
				} else if d.BranchName == "konflux-"+componentKey.Name+"-"+version2Name {
					versionName = version2Name
					baseBranch = "develop"
				}
				Expect(versionName).ToNot(BeEmpty(), "Should match a known version")

				// Verify MergeRequestData fields
				gitURL := SampleRepoLink + "-" + componentKey.Name
				commitMessage := "git-app-name update " + componentKey.Name + ":" + versionName
				title := "git-app-name update " + componentKey.Name + ":" + versionName
				branchName := "konflux-" + componentKey.Name + "-" + versionName
				verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, baseBranch)

				// Each PR should have exactly 2 files: pull and push pipeline
				Expect(len(d.Files)).To(Equal(2))

				expectedPullPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPRSuffix
				expectedPushPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPushSuffix

				// Verify PR contains pipelines with inline spec (from bundle)
				foundPullPipeline := false
				foundPushPipeline := false
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())

					var prYaml tektonapi.PipelineRun
					Expect(yaml.Unmarshal(file.Content, &prYaml)).To(Succeed())

					// When pipeline comes from bundle, spec is embedded inline
					Expect(prYaml.Spec.PipelineSpec).ToNot(BeNil(), "PipelineSpec should be embedded from bundle")
					Expect(prYaml.Spec.PipelineSpec.Tasks).ToNot(BeEmpty(), "Pipeline should have tasks")

					// Verify exact pipeline run names
					if prYaml.Name == expectedPullPipelineName {
						foundPullPipeline = true
					} else if prYaml.Name == expectedPushPipelineName {
						foundPushPipeline = true
					}
				}

				Expect(foundPullPipeline).To(BeTrue(), "PR should contain pull pipeline")
				Expect(foundPushPipeline).To(BeTrue(), "PR should contain push pipeline")
				prCreatedCount++

				// Return different URL based on version
				if d.BranchName == "konflux-"+componentKey.Name+"-"+version1Name {
					return v1PrUrl, nil
				}
				return v2PrUrl, nil
			}

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						AllVersions: true,
					},
				},
			})
			component = createComponent(component)

			// Verify CreateConfiguration action is completely cleared (all 3 variants)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return !component.Spec.Actions.CreateConfiguration.AllVersions &&
					component.Spec.Actions.CreateConfiguration.Version == "" &&
					len(component.Spec.Actions.CreateConfiguration.Versions) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify both versions were onboarded with PR URL
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 2 &&
					component.Status.Versions[0].ConfigurationMergeURL != "" &&
					component.Status.Versions[1].ConfigurationMergeURL != ""
			}, timeout, interval).Should(BeTrue())

			// Verify both versions are onboarded with correct PR URLs
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", v2PrUrl, "")

			Expect(prCreatedCount).To(Equal(2), "Both PRs should have been created exactly once each")

			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
		})

		It("should create PR with PipelineRefName from version-specific Pull and PipelineSpecFromBundle from default config Push using Version", func() {
			versionName := "v1"
			prUrl := "https://githost.com/mr/2345"

			// Mock pipeline content that would be in .tekton/pull-pipeline.yaml
			mockPullPipeline := `
apiVersion: tekton.dev/v1
kind: Pipeline
metadata:
  name: custom-pull-pipeline
spec:
  params:
    - name: git-url
  tasks:
    - name: build
      taskRef:
        name: buildah
`
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				if strings.Contains(filePath, "pull-pipeline.yaml") {
					return []byte(mockPullPipeline), nil
				}
				return []byte{0}, nil
			}

			prCreatedCount := 0
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				// Verify MergeRequestData fields
				gitURL := SampleRepoLink + "-" + componentKey.Name
				commitMessage := "git-app-name update " + componentKey.Name + ":" + versionName
				title := "git-app-name update " + componentKey.Name + ":" + versionName
				branchName := "konflux-" + componentKey.Name + "-" + versionName
				verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, "main")

				// Each PR should have exactly 2 files: pull and push pipeline
				Expect(len(d.Files)).To(Equal(2))

				expectedPullPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPRSuffix
				expectedPushPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPushSuffix

				foundPullPipelineRef := false
				foundPushPipeline := false
				for _, file := range d.Files {
					var prYaml tektonapi.PipelineRun
					Expect(yaml.Unmarshal(file.Content, &prYaml)).To(Succeed())

					// Verify exact pipeline run names and properties
					if prYaml.Name == expectedPullPipelineName {
						foundPullPipelineRef = true
						// Pull: PipelineRefName should create PipelineRef with just name
						Expect(prYaml.Spec.PipelineRef).ToNot(BeNil())
						Expect(prYaml.Spec.PipelineRef.Name).To(Equal("custom-pull-pipeline"))
						Expect(prYaml.Spec.PipelineRef.Resolver).To(BeEmpty(), "PipelineRefName should not use resolver")
						Expect(prYaml.Spec.PipelineSpec).To(BeNil(), "PipelineRefName should use ref, not inline spec")
					} else if prYaml.Name == expectedPushPipelineName {
						foundPushPipeline = true
						// Push: Should use PipelineSpecFromBundle (default config) - inline spec
						Expect(prYaml.Spec.PipelineSpec).ToNot(BeNil(), "Push pipeline should use inline spec from bundle")
						Expect(prYaml.Spec.PipelineSpec.Tasks).ToNot(BeEmpty(), "Push pipeline should have tasks")
					}
				}

				Expect(foundPullPipelineRef).To(BeTrue(), "PR should contain pull pipeline reference by name")
				Expect(foundPushPipeline).To(BeTrue(), "PR should contain push pipeline")
				prCreatedCount++
				return prUrl, nil
			}

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{
						Name:     versionName,
						Revision: "main",
						BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
							Pull: compapiv1alpha1.PipelineDefinition{
								PipelineRefName: "custom-pull-pipeline",
							},
						},
					},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						Version: versionName,
					},
				},
			})
			component = createComponent(component)

			// Verify CreateConfiguration action is completely cleared (all 3 variants)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return !component.Spec.Actions.CreateConfiguration.AllVersions &&
					component.Spec.Actions.CreateConfiguration.Version == "" &&
					len(component.Spec.Actions.CreateConfiguration.Versions) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify version was onboarded with PR URL
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 1 && component.Status.Versions[0].ConfigurationMergeURL != ""
			}, timeout, interval).Should(BeTrue())

			verifyComponentVersionStatus(component.Status.Versions[0], versionName, "main", "succeeded", prUrl, "")

			Expect(prCreatedCount).To(Equal(1), "PR should have been created exactly once")

			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
		})

		It("should create PR with PipelineRefGit from version-specific Push and PipelineSpecFromBundle from default config Pull using Versions", func() {
			versionName := "v1"
			prUrl := "https://githost.com/mr/3456"

			// Mock pipeline content from external git repo
			mockGitPushPipeline := `
apiVersion: tekton.dev/v1
kind: Pipeline
metadata:
  name: external-push-pipeline
spec:
  params:
    - name: git-url
  tasks:
    - name: custom-build
      taskRef:
        name: custom-buildah
`
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				if repoUrl == "https://github.com/custom-pipelines/pipelines.git" && filePath == "pipelines/push.yaml" {
					return []byte(mockGitPushPipeline), nil
				}
				return []byte{0}, nil
			}

			prCreatedCount := 0
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				// Verify MergeRequestData fields
				gitURL := SampleRepoLink + "-" + componentKey.Name
				commitMessage := "git-app-name update " + componentKey.Name + ":" + versionName
				title := "git-app-name update " + componentKey.Name + ":" + versionName
				branchName := "konflux-" + componentKey.Name + "-" + versionName
				verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, "main")

				// Each PR should have exactly 2 files: pull and push pipeline
				Expect(len(d.Files)).To(Equal(2))

				expectedPullPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPRSuffix
				expectedPushPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPushSuffix

				foundPushGitResolver := false
				foundPullPipeline := false
				for _, file := range d.Files {
					var prYaml tektonapi.PipelineRun
					Expect(yaml.Unmarshal(file.Content, &prYaml)).To(Succeed())

					// Verify exact pipeline run names and properties
					if prYaml.Name == expectedPushPipelineName {
						// Push pipeline should use PipelineRefGit with git resolver
						Expect(prYaml.Spec.PipelineRef).ToNot(BeNil())
						Expect(prYaml.Spec.PipelineRef.Resolver).To(Equal(tektonapi.ResolverName("git")))
						foundPushGitResolver = true
						Expect(prYaml.Spec.PipelineRef.Params).ToNot(BeEmpty())

						// Verify git resolver parameters
						var hasUrl, hasRevision, hasPathInRepo bool
						for _, param := range prYaml.Spec.PipelineRef.Params {
							if param.Name == "url" && param.Value.StringVal == "https://github.com/custom-pipelines/pipelines.git" {
								hasUrl = true
							}
							if param.Name == "revision" && param.Value.StringVal == "main" {
								hasRevision = true
							}
							if param.Name == "pathInRepo" && param.Value.StringVal == "pipelines/push.yaml" {
								hasPathInRepo = true
							}
						}
						Expect(hasUrl).To(BeTrue(), "Should have url parameter")
						Expect(hasRevision).To(BeTrue(), "Should have revision parameter")
						Expect(hasPathInRepo).To(BeTrue(), "Should have pathInRepo parameter")

						Expect(prYaml.Spec.PipelineSpec).To(BeNil(), "PipelineRefGit should use ref, not inline spec")
					} else if prYaml.Name == expectedPullPipelineName {
						foundPullPipeline = true
						// Pull: Should use PipelineSpecFromBundle (default config) - inline spec
						Expect(prYaml.Spec.PipelineSpec).ToNot(BeNil(), "Pull pipeline should use inline spec from bundle")
						Expect(prYaml.Spec.PipelineSpec.Tasks).ToNot(BeEmpty(), "Pull pipeline should have tasks")
					}
				}

				Expect(foundPushGitResolver).To(BeTrue(), "PR should contain push pipeline reference with git resolver")
				Expect(foundPullPipeline).To(BeTrue(), "PR should contain pull pipeline")
				prCreatedCount++
				return prUrl, nil
			}

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{
						Name:     versionName,
						Revision: "main",
						BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
							Push: compapiv1alpha1.PipelineDefinition{
								PipelineRefGit: compapiv1alpha1.PipelineRefGit{
									Url:        "https://github.com/custom-pipelines/pipelines.git",
									Revision:   "main",
									PathInRepo: "pipelines/push.yaml",
								},
							},
						},
					},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						Versions: []string{versionName},
					},
				},
			})
			component = createComponent(component)

			// Verify CreateConfiguration action is completely cleared (all 3 variants)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return !component.Spec.Actions.CreateConfiguration.AllVersions &&
					component.Spec.Actions.CreateConfiguration.Version == "" &&
					len(component.Spec.Actions.CreateConfiguration.Versions) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify version was onboarded with PR URL
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 1 && component.Status.Versions[0].ConfigurationMergeURL != ""
			}, timeout, interval).Should(BeTrue())

			verifyComponentVersionStatus(component.Status.Versions[0], versionName, "main", "succeeded", prUrl, "")

			Expect(prCreatedCount).To(Equal(1), "PR should have been created exactly once")

			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
		})

		It("should create PR with PipelineSpecFromBundle from defaultPipeline.PullAndPush using Versions only for specified versions", func() {
			version1Name := "v1"
			version2Name := "v2"
			prUrl := "https://githost.com/mr/7777"

			prCreatedCount := 0
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				// Verify MergeRequestData fields
				gitURL := SampleRepoLink + "-" + componentKey.Name
				commitMessage := "git-app-name update " + componentKey.Name + ":" + version1Name
				title := "git-app-name update " + componentKey.Name + ":" + version1Name
				branchName := "konflux-" + componentKey.Name + "-" + version1Name
				verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, "main")

				// Each PR should have exactly 2 files: pull and push pipeline
				Expect(len(d.Files)).To(Equal(2))

				// Verify PR contains pipelines for v1 only
				foundPullPipeline := false
				foundPushPipeline := false
				expectedPullPipelineName := componentKey.Name + "-" + version1Name + pipelineRunOnPRSuffix
				expectedPushPipelineName := componentKey.Name + "-" + version1Name + pipelineRunOnPushSuffix

				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())

					var prYaml tektonapi.PipelineRun
					Expect(yaml.Unmarshal(file.Content, &prYaml)).To(Succeed())

					// Both pull and push use PipelineSpecFromBundle (from defaultPipeline.PullAndPush)
					Expect(prYaml.Spec.PipelineSpec).ToNot(BeNil(), "Pipeline should use inline spec from bundle")
					Expect(prYaml.Spec.PipelineSpec.Tasks).ToNot(BeEmpty(), "Pipeline should have tasks")

					// Verify pipeline run names for v1 only
					if prYaml.Name == expectedPullPipelineName {
						foundPullPipeline = true
					} else if prYaml.Name == expectedPushPipelineName {
						foundPushPipeline = true
					}
				}

				Expect(foundPullPipeline).To(BeTrue(), "PR should contain pull pipeline for v1")
				Expect(foundPushPipeline).To(BeTrue(), "PR should contain push pipeline for v1")

				prCreatedCount++
				return prUrl, nil
			}

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
				defaultPipeline: compapiv1alpha1.ComponentBuildPipeline{
					PullAndPush: compapiv1alpha1.PipelineDefinition{
						PipelineSpecFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
							Bundle: "latest",
							Name:   defaultPipelineName,
						},
					},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						Versions: []string{version1Name},
					},
				},
			})
			component = createComponent(component)

			// Verify CreateConfiguration action is completely cleared (all 3 variants)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return !component.Spec.Actions.CreateConfiguration.AllVersions &&
					component.Spec.Actions.CreateConfiguration.Version == "" &&
					len(component.Spec.Actions.CreateConfiguration.Versions) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify both versions to be onboarded and v1 to have PR URL
			Eventually(func() bool {
				component = getComponent(componentKey)
				if len(component.Status.Versions) != 2 {
					return false
				}
				// Find v1 and check it has ConfigurationMergeURL
				for _, v := range component.Status.Versions {
					if v.Name == version1Name && v.ConfigurationMergeURL != "" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			// Verify both versions with correct status
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", prUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")

			Expect(prCreatedCount).To(Equal(1), "PR should have been created exactly once")

			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
		})

		It("should create PR only for specified version after initial onboarding and skip PR creation when creation of PR is requested for 2nd time", func() {
			version1Name := "v1"
			version2Name := "v2"
			v1PrUrl := "https://githost.com/mr/1111"

			// Verify SetupPaCWebhookFunc is NOT called when using GitHub App
			SetupPaCWebhookFunc = func(string, string, string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			prCreatedCount := 0
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				// Should only be called for v1
				Expect(d.BranchName).To(Equal("konflux-"+componentKey.Name+"-"+version1Name), "PR should only be created for v1")

				// Verify MergeRequestData fields
				gitURL := SampleRepoLink + "-" + componentKey.Name
				commitMessage := "git-app-name update " + componentKey.Name + ":" + version1Name
				title := "git-app-name update " + componentKey.Name + ":" + version1Name
				branchName := "konflux-" + componentKey.Name + "-" + version1Name
				verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, "main")

				// Each PR should have exactly 2 files: pull and push pipeline
				Expect(len(d.Files)).To(Equal(2))

				expectedPullPipelineName := componentKey.Name + "-" + version1Name + pipelineRunOnPRSuffix
				expectedPushPipelineName := componentKey.Name + "-" + version1Name + pipelineRunOnPushSuffix

				// Verify PR contains pipelines with inline spec (from bundle)
				foundPullPipeline := false
				foundPushPipeline := false
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())

					var prYaml tektonapi.PipelineRun
					Expect(yaml.Unmarshal(file.Content, &prYaml)).To(Succeed())

					// When pipeline comes from bundle, spec is embedded inline
					Expect(prYaml.Spec.PipelineSpec).ToNot(BeNil(), "PipelineSpec should be embedded from bundle")
					Expect(prYaml.Spec.PipelineSpec.Tasks).ToNot(BeEmpty(), "Pipeline should have tasks")

					// Verify exact pipeline run names
					if prYaml.Name == expectedPullPipelineName {
						foundPullPipeline = true
					} else if prYaml.Name == expectedPushPipelineName {
						foundPushPipeline = true
					}
				}

				Expect(foundPullPipeline).To(BeTrue(), "PR should contain pull pipeline")
				Expect(foundPushPipeline).To(BeTrue(), "PR should contain push pipeline")
				prCreatedCount++

				// First call: return PR URL, second call: return empty (files already match)
				if prCreatedCount == 1 {
					return v1PrUrl, nil
				}
				return "", nil
			}

			// Create component without any actions initially
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
			})
			createComponent(component)

			// Wait for both versions to be onboarded (without PR since no actions)
			component = waitForComponentStatusVersions(componentKey, 2)

			// Verify both versions are onboarded without PR URLs
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", "", "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")

			// Now update component to add CreateConfiguration action for v1 only
			component.Spec.Actions.CreateConfiguration.Version = version1Name
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Verify CreateConfiguration action is completely cleared (all 3 variants)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return !component.Spec.Actions.CreateConfiguration.AllVersions &&
					component.Spec.Actions.CreateConfiguration.Version == "" &&
					len(component.Spec.Actions.CreateConfiguration.Versions) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify v1 has PR URL
			Eventually(func() bool {
				component = getComponent(componentKey)
				if len(component.Status.Versions) != 2 {
					return false
				}
				// Find v1 and check it has ConfigurationMergeURL
				for _, v := range component.Status.Versions {
					if v.Name == version1Name && v.ConfigurationMergeURL != "" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			// Verify both versions with correct status
			versionMap = make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")

			Expect(prCreatedCount).To(Equal(1), "PR should have been created exactly once for v1")

			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))

			// Add CreateConfiguration action again for v1 (which already has configuration)
			component = getComponent(componentKey)
			component.Spec.Actions.CreateConfiguration.Version = version1Name
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Verify CreateConfiguration action is cleared again
			Eventually(func() bool {
				component = getComponent(componentKey)
				return !component.Spec.Actions.CreateConfiguration.AllVersions &&
					component.Spec.Actions.CreateConfiguration.Version == "" &&
					len(component.Spec.Actions.CreateConfiguration.Versions) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify both versions with correct status, v1 still has the original PR URL (unchanged)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 2
			}, timeout, interval).Should(BeTrue())

			versionMap = make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")

			// Verify EnsurePaCMergeRequestFunc was called again (prCreatedCount becomes 2)
			// but no new PR was created since files already match
			Expect(prCreatedCount).To(Equal(2), "EnsurePaCMergeRequestFunc should have been called twice")
		})

		It("should handle partial success when second version fails with persistent error while first and third succeed using AllVersions", func() {
			version1Name := "v1"
			version2Name := "v2"
			version3Name := "v3"
			v1PrUrl := "https://github.com/devfile-samples/devfile-sample/pull/1111"
			v3PrUrl := "https://github.com/devfile-samples/devfile-sample/pull/3333"

			v1ConfigPRCreatedCount := 0
			v2ConfigPRAttemptedCount := 0
			v3ConfigPRCreatedCount := 0
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				// Determine which version this PR is for
				var versionName, baseBranch, prUrl string
				if d.BranchName == "konflux-"+componentKey.Name+"-"+version1Name {
					versionName = version1Name
					baseBranch = "main"
					prUrl = v1PrUrl
					v1ConfigPRCreatedCount++
				} else if d.BranchName == "konflux-"+componentKey.Name+"-"+version2Name {
					// v2 fails with branch doesn't exist error
					v2ConfigPRAttemptedCount++
					return "", boerrors.NewBuildOpError(boerrors.EGitLabBranchDoesntExist, fmt.Errorf("branch 'non-existing-branch' does not exist"))
				} else if d.BranchName == "konflux-"+componentKey.Name+"-"+version3Name {
					versionName = version3Name
					baseBranch = "develop"
					prUrl = v3PrUrl
					v3ConfigPRCreatedCount++
				}
				Expect(versionName).ToNot(BeEmpty(), "Should match a known version")

				// Verify MergeRequestData fields
				gitURL := SampleRepoLink + "-" + componentKey.Name
				commitMessage := "git-app-name update " + componentKey.Name + ":" + versionName
				title := "git-app-name update " + componentKey.Name + ":" + versionName
				branchName := "konflux-" + componentKey.Name + "-" + versionName
				verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, baseBranch)

				// Each PR should have exactly 2 files: pull and push pipeline
				Expect(len(d.Files)).To(Equal(2))

				expectedPullPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPRSuffix
				expectedPushPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPushSuffix

				// Verify PR contains both pipelines with inline spec (from bundle)
				foundPullPipeline := false
				foundPushPipeline := false
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())

					var prYaml tektonapi.PipelineRun
					Expect(yaml.Unmarshal(file.Content, &prYaml)).To(Succeed())

					// When pipeline comes from bundle, spec is embedded inline
					Expect(prYaml.Spec.PipelineSpec).ToNot(BeNil(), "PipelineSpec should be embedded from bundle")
					Expect(prYaml.Spec.PipelineSpec.Tasks).ToNot(BeEmpty(), "Pipeline should have tasks")

					// Verify exact pipeline run names
					if prYaml.Name == expectedPullPipelineName {
						foundPullPipeline = true
					} else if prYaml.Name == expectedPushPipelineName {
						foundPushPipeline = true
					}
				}

				Expect(foundPullPipeline).To(BeTrue(), "PR should contain pull pipeline")
				Expect(foundPushPipeline).To(BeTrue(), "PR should contain push pipeline")

				return prUrl, nil
			}

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "non-existing-branch"},
					{Name: version3Name, Revision: "develop"},
				},
			})
			component.Spec.Actions.CreateConfiguration.AllVersions = true
			// Add non-existent versions in Version and Versions to test they are properly discarded
			// (AllVersions has precedence, so these should be silently removed during conversion)
			component.Spec.Actions.CreateConfiguration.Version = "non-existent-version-1"
			component.Spec.Actions.CreateConfiguration.Versions = []string{"non-existent-version-2"}
			createComponent(component)

			// Verify CreateConfiguration action is cleared completely
			// v1 succeeded (removed), v2 failed with persistent error (removed), v3 succeeded (removed)
			// AllVersions is converted to false, Version and Versions are cleared (all versions processed)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return !component.Spec.Actions.CreateConfiguration.AllVersions &&
					component.Spec.Actions.CreateConfiguration.Version == "" &&
					len(component.Spec.Actions.CreateConfiguration.Versions) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify all three versions to be processed: v1 succeeded, v2 failed, v3 succeeded
			Eventually(func() bool {
				component = getComponent(componentKey)
				if len(component.Status.Versions) != 3 {
					return false
				}

				v1Found := false
				v2Failed := false
				v3Found := false
				for _, v := range component.Status.Versions {
					if v.Name == version1Name && v.ConfigurationMergeURL == v1PrUrl && v.OnboardingStatus == "succeeded" {
						v1Found = true
					}
					if v.Name == version2Name && v.OnboardingStatus == "failed" && v.Message != "" && v.OnboardingTime == "" {
						v2Failed = true
					}
					if v.Name == version3Name && v.ConfigurationMergeURL == v3PrUrl && v.OnboardingStatus == "succeeded" {
						v3Found = true
					}
				}
				return v1Found && v2Failed && v3Found
			}, timeout, interval).Should(BeTrue())

			// Verify all three versions in status with correct states
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "non-existing-branch", "failed", "", "94: GitLab branch does not exist")
			Expect(versionMap).To(HaveKey(version3Name))
			verifyComponentVersionStatus(versionMap[version3Name], version3Name, "develop", "succeeded", v3PrUrl, "")

			Expect(v1ConfigPRCreatedCount).To(Equal(1), "v1 PR should have been created exactly once")
			Expect(v2ConfigPRAttemptedCount).To(Equal(1), "v2 should have been attempted exactly once (fails with persistent error)")
			Expect(v3ConfigPRCreatedCount).To(Equal(1), "v3 PR should have been created exactly once")

			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
		})

		It("should handle two transient errors for second version and retry successfully and continue also with third version using AllVersions", func() {
			version1Name := "v1"
			version2Name := "v2"
			version3Name := "v3"
			v1PrUrl := "https://github.com/devfile-samples/devfile-sample/pull/1111"
			v2PrUrl := "https://github.com/devfile-samples/devfile-sample/pull/2222"
			v3PrUrl := "https://github.com/devfile-samples/devfile-sample/pull/3333"

			v1ConfigPRCreatedCount := 0
			v2ConfigPRAttemptedCount := 0
			v3ConfigPRCreatedCount := 0
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				// Determine which version this PR is for
				var versionName, baseBranch, prUrl string
				if d.BranchName == "konflux-"+componentKey.Name+"-"+version1Name {
					versionName = version1Name
					baseBranch = "main"
					prUrl = v1PrUrl
					v1ConfigPRCreatedCount++
				} else if d.BranchName == "konflux-"+componentKey.Name+"-"+version2Name {
					versionName = version2Name
					baseBranch = "develop"
					prUrl = v2PrUrl
					// v2: first and second attempt fails with transient error
					v2ConfigPRAttemptedCount++
					if v2ConfigPRAttemptedCount <= 2 {
						return "", fmt.Errorf("transient network error")
					}
				} else if d.BranchName == "konflux-"+componentKey.Name+"-"+version3Name {
					versionName = version3Name
					baseBranch = "release"
					prUrl = v3PrUrl
					v3ConfigPRCreatedCount++
				}
				Expect(versionName).ToNot(BeEmpty(), "Should match a known version")

				// Verify MergeRequestData fields
				gitURL := SampleRepoLink + "-" + componentKey.Name
				commitMessage := "git-app-name update " + componentKey.Name + ":" + versionName
				title := "git-app-name update " + componentKey.Name + ":" + versionName
				branchName := "konflux-" + componentKey.Name + "-" + versionName
				verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, baseBranch)

				// Each PR should have exactly 2 files: pull and push pipeline
				Expect(len(d.Files)).To(Equal(2))

				expectedPullPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPRSuffix
				expectedPushPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPushSuffix

				// Verify PR contains pipelines with inline spec (from bundle)
				foundPullPipeline := false
				foundPushPipeline := false
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())

					var prYaml tektonapi.PipelineRun
					Expect(yaml.Unmarshal(file.Content, &prYaml)).To(Succeed())

					// When pipeline comes from bundle, spec is embedded inline
					Expect(prYaml.Spec.PipelineSpec).ToNot(BeNil(), "PipelineSpec should be embedded from bundle")
					Expect(prYaml.Spec.PipelineSpec.Tasks).ToNot(BeEmpty(), "Pipeline should have tasks")

					// Verify exact pipeline run names
					if prYaml.Name == expectedPullPipelineName {
						foundPullPipeline = true
					} else if prYaml.Name == expectedPushPipelineName {
						foundPushPipeline = true
					}
				}

				Expect(foundPullPipeline).To(BeTrue(), "PR should contain pull pipeline")
				Expect(foundPushPipeline).To(BeTrue(), "PR should contain push pipeline")

				return prUrl, nil
			}

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
					{Name: version3Name, Revision: "release"},
				},
			})
			component.Spec.Actions.CreateConfiguration.AllVersions = true
			createComponent(component)

			// Verify CreateConfiguration action is completely cleared (all three versions succeeded)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return !component.Spec.Actions.CreateConfiguration.AllVersions &&
					component.Spec.Actions.CreateConfiguration.Version == "" &&
					len(component.Spec.Actions.CreateConfiguration.Versions) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify all three versions eventually succeed (v2 succeeds after retry)
			Eventually(func() bool {
				component = getComponent(componentKey)
				if len(component.Status.Versions) != 3 {
					return false
				}

				v1Found := false
				v2Succeeded := false
				v3Found := false
				for _, v := range component.Status.Versions {
					if v.Name == version1Name && v.ConfigurationMergeURL == v1PrUrl && v.OnboardingStatus == "succeeded" {
						v1Found = true
					}
					if v.Name == version2Name && v.ConfigurationMergeURL == v2PrUrl && v.OnboardingStatus == "succeeded" {
						v2Succeeded = true
					}
					if v.Name == version3Name && v.ConfigurationMergeURL == v3PrUrl && v.OnboardingStatus == "succeeded" {
						v3Found = true
					}
				}
				return v1Found && v2Succeeded && v3Found
			}, timeout, interval).Should(BeTrue())

			// Verify all three versions in status with correct states
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}

			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", v2PrUrl, "")
			Expect(versionMap).To(HaveKey(version3Name))
			verifyComponentVersionStatus(versionMap[version3Name], version3Name, "release", "succeeded", v3PrUrl, "")

			Expect(v1ConfigPRCreatedCount).To(Equal(1), "v1 PR should have been created exactly once")
			Expect(v2ConfigPRAttemptedCount).To(Equal(3), "v2 should have been attempted three times (first failed, second failed, third succeeded)")
			Expect(v3ConfigPRCreatedCount).To(Equal(1), "v3 PR should have been created exactly once")

			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
		})
	})

	// Tests for TriggerBuilds action
	// These tests verify triggering builds via POST to PaC incoming webhook endpoint
	Context("TriggerBuilds action", func() {
		var (
			namespace    = "trigger-builds-ns"
			componentKey = types.NamespacedName{Name: "test-component", Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()

			// Mock HTTP client for PaC webhook POST requests
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			gock.InterceptClient(client)
			GetHttpClientFunction = func() *http.Client {
				return client
			}
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
			gock.Off()
		})

		It("should trigger build for single version using TriggerBuild field", func() {
			version1Name := "v1"
			version2Name := "v2"

			// Mock HTTP POST request
			var capturedPostData map[string]interface{}
			webhookTargetUrl := "https://" + pacHost

			gock.New(webhookTargetUrl).
				Post("/incoming").
				SetMatcher(gock.NewBasicMatcher()).
				AddMatcher(gock.MatchFunc(func(req *http.Request, _ *gock.Request) (bool, error) {
					defer GinkgoRecover()
					var bodyJson map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&bodyJson); err != nil {
						return false, err
					}
					capturedPostData = bodyJson
					return true, nil
				})).
				Reply(202).JSON(map[string]interface{}{})

			// First onboard versions
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
			})
			createComponent(component)

			// Wait for versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 2)

			// Get repository name for verification
			repositoryName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
			Expect(err).NotTo(HaveOccurred())

			// Now add trigger build action using TriggerBuild field
			component.Spec.Actions.TriggerBuild = version1Name
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Get incoming secret for verification (created after trigger)
			incomingSecretName := getPaCIncomingSecretName(repositoryName)
			incomingSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretKey)

			var incomingSecret corev1.Secret
			Expect(k8sClient.Get(ctx, incomingSecretKey, &incomingSecret)).To(Succeed())
			secretValue := string(incomingSecret.Data[pacIncomingSecretKey])

			// TriggerBuild action should be removed after successful trigger
			component = waitForComponentSpecActionEmpty(componentKey, "trigger", "build")

			// Verify HTTP POST request was made
			Eventually(func() bool {
				return capturedPostData != nil
			}, timeout, interval).Should(BeTrue())

			// Verify all POST request fields
			v1PipelineRunName := component.Name + "-" + version1Name + pipelineRunOnPushSuffix
			verifyPacWebhookIncomingPostData(capturedPostData, repositoryName, secretValue, v1PipelineRunName, namespace, "main", component.Spec.Source.GitURL)

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			repository := validatePaCRepository(component, "", "")

			// Verify Repository.Spec.Incomings was updated
			validatePaCRepositoryIncomings(repository, []string{"main"})
		})

		It("should trigger builds for subset of onboarded versions with different revisions", func() {
			version1Name := "v1"
			version2Name := "v2"
			version3Name := "v3"

			// Mock HTTP POST requests
			var capturedPostData []map[string]interface{}
			webhookTargetUrl := "https://" + pacHost

			gock.New(webhookTargetUrl).
				Post("/incoming").
				Times(2). // Expect 2 POST requests (one for v1, one for v2)
				SetMatcher(gock.NewBasicMatcher()).
				AddMatcher(gock.MatchFunc(func(req *http.Request, _ *gock.Request) (bool, error) {
					defer GinkgoRecover()
					var bodyJson map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&bodyJson); err != nil {
						return false, err
					}
					capturedPostData = append(capturedPostData, bodyJson)
					return true, nil
				})).
				Reply(202).JSON(map[string]interface{}{})

			// First onboard versions
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
					{Name: version3Name, Revision: "release"},
				},
			})
			createComponent(component)

			// Wait for versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 3)

			// Get repository name for verification
			repositoryName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
			Expect(err).NotTo(HaveOccurred())

			// Now add trigger build action
			component.Spec.Actions.TriggerBuilds = []string{version1Name, version2Name}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Get incoming secret for verification (created after trigger)
			incomingSecretName := getPaCIncomingSecretName(repositoryName)
			incomingSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretKey)

			var incomingSecret corev1.Secret
			Expect(k8sClient.Get(ctx, incomingSecretKey, &incomingSecret)).To(Succeed())
			secretValue := string(incomingSecret.Data[pacIncomingSecretKey])

			// TriggerBuilds action should be removed after successful trigger
			component = waitForComponentSpecActionEmpty(componentKey, "trigger", "builds")

			// Verify HTTP POST requests were made
			Eventually(func() int {
				return len(capturedPostData)
			}, timeout, interval).Should(Equal(2))

			// Verify POST data for both versions
			v1PipelineRunName := component.Name + "-" + version1Name + pipelineRunOnPushSuffix
			v2PipelineRunName := component.Name + "-" + version2Name + pipelineRunOnPushSuffix

			// Find which request is for which version
			var v1Data, v2Data map[string]interface{}
			for _, data := range capturedPostData {
				pipelinerun := data["pipelinerun"].(string)
				if pipelinerun == v1PipelineRunName {
					v1Data = data
				} else if pipelinerun == v2PipelineRunName {
					v2Data = data
				}
			}

			Expect(v1Data).NotTo(BeNil(), "v1 POST request should exist")
			Expect(v2Data).NotTo(BeNil(), "v2 POST request should exist")

			// Verify v1 POST request
			verifyPacWebhookIncomingPostData(v1Data, repositoryName, secretValue, v1PipelineRunName, namespace, "main", component.Spec.Source.GitURL)
			// Verify v2 POST request
			verifyPacWebhookIncomingPostData(v2Data, repositoryName, secretValue, v2PipelineRunName, namespace, "develop", component.Spec.Source.GitURL)

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			repository := validatePaCRepository(component, "", "")

			// Verify Repository.Spec.Incomings was updated
			validatePaCRepositoryIncomings(repository, []string{"main", "develop"})
		})

		It("should trigger builds for subset of onboarded versions with same revision", func() {
			version1Name := "v1"
			version2Name := "v2"
			version3Name := "v3"

			// Mock HTTP POST requests
			var capturedPostData []map[string]interface{}
			webhookTargetUrl := "https://" + pacHost

			gock.New(webhookTargetUrl).
				Post("/incoming").
				Times(2). // Expect 2 POST requests (one for v1, one for v2)
				SetMatcher(gock.NewBasicMatcher()).
				AddMatcher(gock.MatchFunc(func(req *http.Request, _ *gock.Request) (bool, error) {
					defer GinkgoRecover()
					var bodyJson map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&bodyJson); err != nil {
						return false, err
					}
					capturedPostData = append(capturedPostData, bodyJson)
					return true, nil
				})).
				Reply(202).JSON(map[string]interface{}{})

			// First onboard versions - all using same revision
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "main"},
					{Name: version3Name, Revision: "main"},
				},
			})
			createComponent(component)

			// Wait for versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 3)

			// Get repository name for verification
			repositoryName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
			Expect(err).NotTo(HaveOccurred())

			// Now add trigger build action
			component.Spec.Actions.TriggerBuilds = []string{version1Name, version2Name}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Get incoming secret for verification (created after trigger)
			incomingSecretName := getPaCIncomingSecretName(repositoryName)
			incomingSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretKey)

			var incomingSecret corev1.Secret
			Expect(k8sClient.Get(ctx, incomingSecretKey, &incomingSecret)).To(Succeed())
			secretValue := string(incomingSecret.Data[pacIncomingSecretKey])

			// TriggerBuilds action should be removed after successful trigger
			component = waitForComponentSpecActionEmpty(componentKey, "trigger", "builds")

			// Verify HTTP POST requests were made
			Eventually(func() int {
				return len(capturedPostData)
			}, timeout, interval).Should(Equal(2))

			// Verify POST data for both versions
			v1PipelineRunName := component.Name + "-" + version1Name + pipelineRunOnPushSuffix
			v2PipelineRunName := component.Name + "-" + version2Name + pipelineRunOnPushSuffix

			// Find which request is for which version
			var v1Data, v2Data map[string]interface{}
			for _, data := range capturedPostData {
				pipelinerun := data["pipelinerun"].(string)
				if pipelinerun == v1PipelineRunName {
					v1Data = data
				} else if pipelinerun == v2PipelineRunName {
					v2Data = data
				}
			}

			Expect(v1Data).NotTo(BeNil(), "v1 POST request should exist")
			Expect(v2Data).NotTo(BeNil(), "v2 POST request should exist")

			// Verify v1 POST request
			verifyPacWebhookIncomingPostData(v1Data, repositoryName, secretValue, v1PipelineRunName, namespace, "main", component.Spec.Source.GitURL)
			// Verify v2 POST request
			verifyPacWebhookIncomingPostData(v2Data, repositoryName, secretValue, v2PipelineRunName, namespace, "main", component.Spec.Source.GitURL)

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			repository := validatePaCRepository(component, "", "")

			// Verify Repository.Spec.Incomings was updated - only "main" since all versions use same branch
			validatePaCRepositoryIncomings(repository, []string{"main"})
		})

		It("should trigger builds and offboard one triggered version simultaneously", func() {
			version1Name := "v1"
			version2Name := "v2"
			version3Name := "v3"

			// Mock HTTP POST requests
			var capturedPostData []map[string]interface{}
			webhookTargetUrl := "https://" + pacHost

			gock.New(webhookTargetUrl).
				Post("/incoming").
				Times(1). // Expect 1 POST request (only for v2, v1 is being offboarded)
				SetMatcher(gock.NewBasicMatcher()).
				AddMatcher(gock.MatchFunc(func(req *http.Request, _ *gock.Request) (bool, error) {
					defer GinkgoRecover()
					var bodyJson map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&bodyJson); err != nil {
						return false, err
					}
					capturedPostData = append(capturedPostData, bodyJson)
					return true, nil
				})).
				Reply(202).JSON(map[string]interface{}{})

			// First onboard versions
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
					{Name: version3Name, Revision: "release"},
				},
			})
			createComponent(component)

			// Wait for versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 3)

			// Get repository name for verification
			repositoryName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
			Expect(err).NotTo(HaveOccurred())

			// Now add trigger build action and offboard v1 at the same time
			component.Spec.Actions.TriggerBuilds = []string{version1Name, version2Name}
			component.Spec.Source.Versions = []compapiv1alpha1.ComponentVersion{
				{Name: version2Name, Revision: "develop"},
				{Name: version3Name, Revision: "release"},
			}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Get incoming secret for verification (created after trigger)
			incomingSecretName := getPaCIncomingSecretName(repositoryName)
			incomingSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretKey)

			var incomingSecret corev1.Secret
			Expect(k8sClient.Get(ctx, incomingSecretKey, &incomingSecret)).To(Succeed())
			secretValue := string(incomingSecret.Data[pacIncomingSecretKey])

			// TriggerBuilds action should be removed after successful trigger
			component = waitForComponentSpecActionEmpty(componentKey, "trigger", "builds")

			// Verify HTTP POST request was made only for v2 (v1 was being offboarded)
			Eventually(func() int {
				return len(capturedPostData)
			}, timeout, interval).Should(Equal(1))

			// Verify POST data for v2 only
			v2PipelineRunName := component.Name + "-" + version2Name + pipelineRunOnPushSuffix

			Expect(len(capturedPostData)).To(Equal(1))
			v2Data := capturedPostData[0]
			Expect(v2Data["pipelinerun"]).To(Equal(v2PipelineRunName))

			// Verify v2 POST request
			verifyPacWebhookIncomingPostData(v2Data, repositoryName, secretValue, v2PipelineRunName, namespace, "develop", component.Spec.Source.GitURL)

			// Verify v1 is removed from status.versions
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 2
			}, timeout, interval).Should(BeTrue())

			// Verify only v2 and v3 remain
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}
			Expect(versionMap).NotTo(HaveKey(version1Name))
			Expect(versionMap).To(HaveKey(version2Name))
			Expect(versionMap).To(HaveKey(version3Name))

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			repository := validatePaCRepository(component, "", "")

			// Verify Repository.Spec.Incomings updated - only v2's branch "devel"
			validatePaCRepositoryIncomings(repository, []string{"develop"})
		})

		It("should handle partial success when one version trigger fails then succeeds on retry", func() {
			version1Name := "v1"
			version2Name := "v2"

			// Track which POST requests were made
			var capturedPostData []map[string]interface{}
			webhookTargetUrl := "https://" + pacHost

			// Create matcher function that captures all requests
			captureRequestMatcher := gock.MatchFunc(func(req *http.Request, _ *gock.Request) (bool, error) {
				defer GinkgoRecover()
				var bodyJson map[string]interface{}
				if err := json.NewDecoder(req.Body).Decode(&bodyJson); err != nil {
					return false, err
				}
				capturedPostData = append(capturedPostData, bodyJson)
				return true, nil
			})

			// Mock HTTP POST requests - gock consumes mocks in FIFO order
			// 1st request: succeed (v1)
			gock.New(webhookTargetUrl).
				Post("/incoming").
				SetMatcher(gock.NewBasicMatcher()).
				AddMatcher(captureRequestMatcher).
				Reply(202).JSON(map[string]interface{}{})

			// 2nd request: fail (v2 first attempt)
			gock.New(webhookTargetUrl).
				Post("/incoming").
				SetMatcher(gock.NewBasicMatcher()).
				AddMatcher(captureRequestMatcher).
				Reply(500).JSON(map[string]interface{}{"error": "internal server error"})

			// 3rd request: succeed (v2 retry)
			gock.New(webhookTargetUrl).
				Post("/incoming").
				SetMatcher(gock.NewBasicMatcher()).
				AddMatcher(captureRequestMatcher).
				Reply(202).JSON(map[string]interface{}{})

			// First onboard both versions
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
			})
			createComponent(component)

			// Wait for versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 2)

			// Trigger builds for both versions
			component.Spec.Actions.TriggerBuilds = []string{version1Name, version2Name}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for all actions to be cleared (v1 succeeds, v2 fails then retries successfully)
			component = waitForComponentSpecActionEmpty(componentKey, "trigger", "builds")

			// Verify v2 POST succeeded on second attempt (3 total: v1, v2 failed, v2 success)
			Eventually(func() int {
				return len(capturedPostData)
			}, timeout, interval).Should(Equal(3))

			// Get repository name for verification
			repositoryName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
			Expect(err).NotTo(HaveOccurred())

			// Get incoming secret
			incomingSecretName := getPaCIncomingSecretName(repositoryName)
			incomingSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretKey)

			var incomingSecret corev1.Secret
			Expect(k8sClient.Get(ctx, incomingSecretKey, &incomingSecret)).To(Succeed())
			secretValue := string(incomingSecret.Data[pacIncomingSecretKey])

			// Verify POST requests were made with correct data
			// We have 3 POSTs: v1 (success), v2 (failed with 500), v2 (retry success)
			v1PipelineRunName := component.Name + "-" + version1Name + pipelineRunOnPushSuffix
			v2PipelineRunName := component.Name + "-" + version2Name + pipelineRunOnPushSuffix

			// Count POST requests by version
			v1Count := 0
			v2Count := 0
			var v1Data, v2Data map[string]interface{}
			for _, data := range capturedPostData {
				pipelinerun := data["pipelinerun"].(string)
				if pipelinerun == v1PipelineRunName {
					v1Count++
					v1Data = data
				} else if pipelinerun == v2PipelineRunName {
					v2Count++
					v2Data = data // Keep the last v2 request (the successful retry)
				}
			}

			Expect(v1Count).To(Equal(1), "v1 should have been triggered once")
			Expect(v2Count).To(Equal(2), "v2 should have been triggered twice (failed + retry)")
			Expect(v1Data).NotTo(BeNil(), "v1 POST request should exist")
			Expect(v2Data).NotTo(BeNil(), "v2 POST request should exist")

			// Verify v1 POST request
			verifyPacWebhookIncomingPostData(v1Data, repositoryName, secretValue, v1PipelineRunName, namespace, "main", component.Spec.Source.GitURL)
			// Verify v2 POST request (either attempt has same data)
			verifyPacWebhookIncomingPostData(v2Data, repositoryName, secretValue, v2PipelineRunName, namespace, "develop", component.Spec.Source.GitURL)

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			repository := validatePaCRepository(component, "", "")

			// Verify Repository.Spec.Incomings was updated with both branches
			validatePaCRepositoryIncomings(repository, []string{"main", "develop"})
		})
	})

	// Tests for webhook secret management with token-based authentication
	// These tests verify webhook secrets are created and reused correctly for components sharing the same repository
	Context("Webhook secret management", func() {
		var (
			namespace        = "webhook-secret-ns"
			component1Key    = types.NamespacedName{Name: "component-1", Namespace: namespace}
			component2Key    = types.NamespacedName{Name: "component-2", Namespace: namespace}
			webhookSecretKey = types.NamespacedName{Name: pipelinesAsCodeWebhooksSecretName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)

			// Use token-based auth instead of GitHub App to test ensureWebhookSecret
			scmSecretKey := types.NamespacedName{Name: "github-token-secret", Namespace: namespace}
			scmSecretData := map[string]string{
				"password": "test-token",
			}
			labels := map[string]string{
				"appstudio.redhat.com/credentials": "scm",
				"appstudio.redhat.com/scm.host":    "github.com",
			}
			createSCMSecret(scmSecretKey, scmSecretData, corev1.SecretTypeBasicAuth, nil, labels)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			// Delete webhook secret to ensure it's created fresh in each test
			deleteSecret(webhookSecretKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(component1Key)
			deleteComponentAndOwnedResources(component2Key)
		})

		It("should reuse webhook secret for components in same repository", func() {
			versionName := "v1"

			sameGitURL := "https://github.com/test-org/shared-repo"

			// Track SetupPaCWebhook calls
			webhookSetupCount := 0
			var capturedWebhookSecrets []string
			SetupPaCWebhookFunc = func(repoUrl, webhookTargetUrl, webhookSecret string) error {
				webhookSetupCount++
				capturedWebhookSecrets = append(capturedWebhookSecrets, webhookSecret)
				Expect(repoUrl).To(Equal(sameGitURL))
				Expect(webhookTargetUrl).ToNot(BeEmpty())
				Expect(webhookSecret).ToNot(BeEmpty())
				return nil
			}

			// Create first component
			component1 := getComponentData(componentConfig{
				componentKey: component1Key,
				gitURL:       sameGitURL,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
			})
			createComponent(component1)

			// Wait for onboarding
			component1 = waitForComponentStatusVersions(component1Key, 1)

			// Verify SetupPaCWebhook was called once for first component
			Expect(webhookSetupCount).To(Equal(1), "SetupPaCWebhook should be called once for first component")

			// Verify webhook secret was created with key for this git URL
			webhookSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, webhookSecretKey, webhookSecret)).To(Succeed())

			// Get expected key using the same function as the controller
			expectedKey := getWebhookSecretKeyForComponent(*component1, true)
			Expect(webhookSecret.Data).To(HaveKey(expectedKey))
			firstSecretValue := string(webhookSecret.Data[expectedKey])
			Expect(firstSecretValue).NotTo(BeEmpty())
			Expect(len(webhookSecret.Data)).To(Equal(1), "Should have only one webhook secret key")

			// Create second component with same git URL
			component2 := getComponentData(componentConfig{
				componentKey: component2Key,
				gitURL:       sameGitURL,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "develop"},
				},
			})
			createComponent(component2)

			// Both components should be onboarded successfully (reusing webhook)
			waitForComponentStatusVersions(component2Key, 1)

			// Verify webhook secret still has only one key (shared between components)
			Expect(k8sClient.Get(ctx, webhookSecretKey, webhookSecret)).To(Succeed())
			Expect(len(webhookSecret.Data)).To(Equal(1), "Should still have only one webhook secret key")
			Expect(webhookSecret.Data).To(HaveKey(expectedKey))
			Expect(string(webhookSecret.Data[expectedKey])).To(Equal(firstSecretValue), "Webhook secret value should be reused")

			// Verify SetupPaCWebhook was called for both components
			Expect(webhookSetupCount).To(Equal(2), "SetupPaCWebhook should be called once per component")
			// Verify both calls used the same webhook secret (reused for same repository)
			Expect(len(capturedWebhookSecrets)).To(Equal(2))
			Expect(capturedWebhookSecrets[0]).To(Equal(capturedWebhookSecrets[1]), "Both components should use the same webhook secret")
		})

		It("should use different webhook secrets for components with different git URLs", func() {
			versionName := "v1"

			gitURL1 := "https://github.com/test-org/repo1"
			gitURL2 := "https://github.com/test-org/repo2"

			// Track SetupPaCWebhook calls
			webhookSetupCount := 0
			var capturedWebhookSecrets []string
			var capturedRepoURLs []string
			SetupPaCWebhookFunc = func(repoUrl, webhookTargetUrl, webhookSecret string) error {
				webhookSetupCount++
				capturedWebhookSecrets = append(capturedWebhookSecrets, webhookSecret)
				capturedRepoURLs = append(capturedRepoURLs, repoUrl)
				Expect(webhookTargetUrl).ToNot(BeEmpty())
				Expect(webhookSecret).ToNot(BeEmpty())
				return nil
			}

			// Create first component with one git URL
			component1 := getComponentData(componentConfig{
				componentKey: component1Key,
				gitURL:       gitURL1,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
			})
			createComponent(component1)

			// Wait for onboarding
			component1 = waitForComponentStatusVersions(component1Key, 1)

			// Verify SetupPaCWebhook was called once for first component
			Expect(webhookSetupCount).To(Equal(1), "SetupPaCWebhook should be called once for first component")
			Expect(capturedRepoURLs[0]).To(Equal(gitURL1))

			// Verify webhook secret was created with key for first git URL
			webhookSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, webhookSecretKey, webhookSecret)).To(Succeed())

			expectedKey1 := getWebhookSecretKeyForComponent(*component1, true)
			Expect(webhookSecret.Data).To(HaveKey(expectedKey1))
			secretValue1 := string(webhookSecret.Data[expectedKey1])
			Expect(secretValue1).NotTo(BeEmpty())
			Expect(len(webhookSecret.Data)).To(Equal(1), "Should have one webhook secret key after first component")

			// Create second component with different git URL
			component2 := getComponentData(componentConfig{
				componentKey: component2Key,
				gitURL:       gitURL2,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
			})
			createComponent(component2)

			// Both should be onboarded successfully with different webhooks
			component2 = waitForComponentStatusVersions(component2Key, 1)

			// Verify webhook secret now has two different keys
			Expect(k8sClient.Get(ctx, webhookSecretKey, webhookSecret)).To(Succeed())
			Expect(len(webhookSecret.Data)).To(Equal(2), "Should have two webhook secret keys for different repos")

			expectedKey2 := getWebhookSecretKeyForComponent(*component2, true)
			Expect(webhookSecret.Data).To(HaveKey(expectedKey1))
			Expect(webhookSecret.Data).To(HaveKey(expectedKey2))

			// Verify the secret values are different
			secretValue2 := string(webhookSecret.Data[expectedKey2])
			Expect(secretValue2).NotTo(BeEmpty())
			Expect(secretValue2).NotTo(Equal(secretValue1), "Different repositories should have different webhook secrets")

			// Verify SetupPaCWebhook was called for both components
			Expect(webhookSetupCount).To(Equal(2), "SetupPaCWebhook should be called once per component")
			Expect(capturedRepoURLs[0]).To(Equal(gitURL1))
			Expect(capturedRepoURLs[1]).To(Equal(gitURL2))
			// Verify the two calls used different webhook secrets (different repositories)
			Expect(len(capturedWebhookSecrets)).To(Equal(2))
			Expect(capturedWebhookSecrets[0]).NotTo(Equal(capturedWebhookSecrets[1]), "Different repositories should use different webhook secrets")
		})
	})

	// Tests for token-based authentication across different git providers
	// These tests verify onboarding, trigger builds, and configuration creation using token authentication (GitLab, GitHub, Forgejo)
	Context("Token-based authentication", func() {
		_ = BeforeEach(func() {
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		// TokenBasedAuthScenario holds the configuration for a token-based authentication test scenario
		type TokenBasedAuthScenario struct {
			name          string
			namespace     string
			gitURL        string
			scmSecretName string
			scmHost       string
			gitProvider   string // Optional: only set for providers that need git-provider annotation (e.g., "forgejo")
		}

		// createTokenBasedAuthContextRun creates test scenarios for token-based authentication
		createTokenBasedAuthContextRun := func(data TokenBasedAuthScenario) {
			Context(data.name, func() {
				var (
					component        *compapiv1alpha1.Component
					namespace        = data.namespace
					componentKey     = types.NamespacedName{Name: "test-component", Namespace: namespace}
					scmSecretKey     = types.NamespacedName{Name: data.scmSecretName, Namespace: namespace}
					webhookSecretKey = types.NamespacedName{Name: pipelinesAsCodeWebhooksSecretName, Namespace: namespace}
				)

				_ = BeforeEach(func() {
					createNamespace(namespace)
					// Create SCM secret for token authentication
					scmSecretData := map[string]string{
						"password": "test-token",
					}
					labels := map[string]string{
						"appstudio.redhat.com/credentials": "scm",
						"appstudio.redhat.com/scm.host":    data.scmHost,
					}
					createSCMSecret(scmSecretKey, scmSecretData, corev1.SecretTypeBasicAuth, nil, labels)
				})

				It("should successfully onboard component using token authentication", func() {
					gitURL := data.gitURL

					// Track SetupPaCWebhook calls
					webhookSetupCount := 0
					SetupPaCWebhookFunc = func(repoUrl, webhookTargetUrl, webhookSecret string) error {
						webhookSetupCount++
						Expect(repoUrl).To(Equal(gitURL))
						Expect(webhookTargetUrl).ToNot(BeEmpty())
						Expect(webhookSecret).ToNot(BeEmpty())
						return nil
					}

					componentCfg := componentConfig{
						componentKey: componentKey,
						gitURL:       gitURL,
						versions: []compapiv1alpha1.ComponentVersion{
							{Name: "v1", Revision: "main"},
						},
					}
					// Add git-provider annotation if specified
					if data.gitProvider != "" {
						componentCfg.annotations = map[string]string{
							"git-provider": data.gitProvider,
						}
					}

					component = getComponentData(componentCfg)
					component = createComponent(component)

					// Component should be onboarded successfully with token auth
					component = waitForComponentStatusVersions(componentKey, 1)

					// Verify component version status
					verifyComponentVersionStatus(component.Status.Versions[0], "v1", "main", "succeeded", "", "")

					// Verify SetupPaCWebhook was called
					Expect(webhookSetupCount).To(Equal(1), "SetupPaCWebhook should be called once for token-based auth")

					// Verify webhook secret was created with correct key
					webhookSecret := &corev1.Secret{}
					Expect(k8sClient.Get(ctx, webhookSecretKey, webhookSecret)).To(Succeed())

					expectedKey := getWebhookSecretKeyForComponent(*component, true)
					Expect(webhookSecret.Data).To(HaveKey(expectedKey))
					Expect(string(webhookSecret.Data[expectedKey])).NotTo(BeEmpty())

					// Verify Repository CR was created with correct URL and GitProvider (token auth)
					validatePaCRepository(component, scmSecretKey.Name, "password")
				})

				It("should successfully trigger builds using token authentication", func() {
					// Get component (should exist from onboarding test)
					component = getComponent(componentKey)
					Expect(len(component.Status.Versions)).To(Equal(1), "Component should be onboarded")

					// Trigger build
					component.Spec.Actions.TriggerBuild = "v1"
					Expect(k8sClient.Update(ctx, component)).To(Succeed())

					// Build should be triggered successfully
					component = waitForComponentSpecActionEmpty(componentKey, "trigger", "build")

					// Verify Repository CR and Incomings was updated
					repository := validatePaCRepository(component, scmSecretKey.Name, "password")
					validatePaCRepositoryIncomings(repository, []string{"main"})
				})

				It("should successfully create configuration PR using token authentication", func() {
					// Get component (should exist from onboarding test)
					component = getComponent(componentKey)
					Expect(len(component.Status.Versions)).To(Equal(1), "Component should be onboarded")

					// Set CreateConfiguration action
					component.Spec.Actions.CreateConfiguration = compapiv1alpha1.ComponentCreatePipelineConfiguration{
						AllVersions: true,
					}
					Expect(k8sClient.Update(ctx, component)).To(Succeed())

					// Configuration should be created successfully
					component = waitForComponentSpecActionEmpty(componentKey, "create-pr", "allversions")

					// Verify configuration merge URL is not empty
					verifyComponentVersionStatus(component.Status.Versions[0], "v1", "main", "succeeded", "*", "")

					// Clean up component at the end of this sub-context
					deleteComponentAndOwnedResources(componentKey)
				})
			})
		}

		tokenBasedAuthScenarios := []TokenBasedAuthScenario{
			{
				name:          "GitLab",
				gitURL:        "https://gitlab.com/test-org/test-repo",
				scmSecretName: "gitlab-token-secret",
				scmHost:       "gitlab.com",
				namespace:     "token-auth-gitlab-ns",
			},
			{
				name:          "GitHub",
				gitURL:        "https://github.com/test-org/test-repo",
				scmSecretName: "github-token-secret",
				scmHost:       "github.com",
				namespace:     "token-auth-github-ns",
			},
			{
				name:          "Forgejo",
				gitURL:        "https://gitforgejo.com/test-org/test-repo",
				scmSecretName: "forgejo-token-secret",
				scmHost:       "gitforgejo.com",
				namespace:     "token-auth-forgejo-ns",
				gitProvider:   "forgejo",
			},
		}

		for _, scn := range tokenBasedAuthScenarios {
			s := scn // save the iterator in a var to prevent overriding with the next iteration
			createTokenBasedAuthContextRun(s)
		}
	})

	// Tests for component deletion and cleanup
	// These tests verify webhook deletion, branch cleanup, and merge request creation during component deletion
	Context("Component deletion and cleanup", func() {
		var (
			namespace    = "cleanup-ns"
			componentKey = types.NamespacedName{Name: "test-component", Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
		})

		It("should not block deletion if cleanup fails", func() {
			createComponent(getSampleComponentData(componentKey))

			// Wait for onboarding
			component := waitForComponentStatusVersions(componentKey, 1)

			// Verify component version status
			verifyComponentVersionStatus(component.Status.Versions[0], "version1", "main", "succeeded", "", "")

			// Mock DeleteBranch to simulate cleanup failure
			DeleteBranchFunc = func(repoUrl, branchName string) (bool, error) {
				return false, fmt.Errorf("simulated branch deletion failure")
			}

			// Delete component - should succeed even if cleanup has issues
			deleteComponent(componentKey)
		})

		It("should not attempt to create service account during deletion", func() {
			createComponent(getSampleComponentData(componentKey))

			// Wait for onboarding to complete
			component := waitForComponentStatusVersions(componentKey, 1)

			// Verify component version status
			verifyComponentVersionStatus(component.Status.Versions[0], "version1", "main", "succeeded", "", "")

			// Verify service account exists
			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			deleteServiceAccount(saKey)
			deleteComponent(componentKey)

			// Verify service account was not recreated during deletion
			var sa corev1.ServiceAccount
			err := k8sClient.Get(ctx, saKey, &sa)
			Expect(errors.IsNotFound(err)).To(BeTrue(), "Service account should not be recreated during component deletion")
		})

		It("should remove incomings from Repository on deletion", func() {
			versionName := "v1"

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
			})
			component = createComponent(component)

			// Wait for onboarding to complete
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 1 && len(component.Finalizers) == 1
			}, timeout, interval).Should(BeTrue())

			// Verify component version status
			verifyComponentVersionStatus(component.Status.Versions[0], versionName, "main", "succeeded", "", "")

			// Verify resources were created
			repositoryName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryKey := types.NamespacedName{Namespace: component.Namespace, Name: repositoryName}

			// Repository CR should exist
			repository := waitPaCRepositoryCreated(repositoryKey)
			Expect(repository).NotTo(BeNil())

			// Manually add incomings to Repository to test cleanup
			// (Incomings are normally created during TriggerBuild, but we add them manually here
			// to test that component deletion properly removes them)
			incomingSecretName := getPaCIncomingSecretName(repositoryName)
			incomings := []pacv1alpha1.Incoming{{
				Type:    "webhook-url",
				Secret:  pacv1alpha1.Secret{Name: incomingSecretName, Key: pacIncomingSecretKey},
				Targets: []string{"main"},
				Params:  []string{"source_url"},
			}}
			repository.Spec.Incomings = &incomings
			Expect(k8sClient.Update(ctx, repository)).To(Succeed())

			// Verify Repository has incomings
			Eventually(func() bool {
				var repo pacv1alpha1.Repository
				if err := k8sClient.Get(ctx, repositoryKey, &repo); err != nil {
					return false
				}
				return repo.Spec.Incomings != nil && len(*repo.Spec.Incomings) > 0
			}, timeout, interval).Should(BeTrue())

			// Now delete the component
			deleteComponent(componentKey)

			// Incomings should be removed or cleared from Repository
			Eventually(func() bool {
				var repo pacv1alpha1.Repository
				if err := k8sClient.Get(ctx, repositoryKey, &repo); err != nil {
					return false
				}
				if repo.Spec.Incomings == nil {
					return true
				}
				// Check that incoming secret for this component is removed or has empty targets
				for _, incoming := range *repo.Spec.Incomings {
					if incoming.Secret.Name == incomingSecretName {
						// Incoming entry exists, check if targets are empty (cleanup completed)
						return len(incoming.Targets) == 0
					}
				}
				// Incoming entry not found (also acceptable)
				return true
			}, timeout, interval).Should(BeTrue())
		})

		It("should call DeletePaCWebhook on deletion with token auth", func() {
			versionName := "v1"

			// Create SCM secret for webhook authentication
			scmSecretKey := types.NamespacedName{Name: "gitlab-token-secret", Namespace: namespace}
			scmSecretData := map[string]string{
				"password": "test-token",
			}
			labels := map[string]string{
				"appstudio.redhat.com/credentials": "scm",
				"appstudio.redhat.com/scm.host":    "gitlab.com",
			}
			createSCMSecret(scmSecretKey, scmSecretData, corev1.SecretTypeBasicAuth, nil, labels)
			defer deleteSecret(scmSecretKey)

			component := getComponentData(componentConfig{
				componentKey: componentKey,
				gitURL:       "https://gitlab.com/test-org/test-repo",
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: versionName, Revision: "main"},
				},
			})
			component = createComponent(component)

			// Wait for onboarding to complete
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Status.Versions) == 1 && len(component.Finalizers) == 1
			}, timeout, interval).Should(BeTrue())

			// Verify component version status
			verifyComponentVersionStatus(component.Status.Versions[0], versionName, "main", "succeeded", "", "")

			// Set up DeletePaCWebhook mock to verify it's called
			isDeletePaCWebhookCalled := false
			DeletePaCWebhookFunc = func(repoUrl string, webhookUrl string) error {
				defer GinkgoRecover()
				isDeletePaCWebhookCalled = true
				Expect(repoUrl).To(Equal(component.Spec.Source.GitURL))
				Expect(webhookUrl).To(Equal("https://" + pacHost))
				return nil
			}

			deleteComponent(componentKey)

			// DeletePaCWebhook should be called with token-based auth
			Eventually(func() bool {
				return isDeletePaCWebhookCalled
			}, timeout, interval).Should(BeTrue())
		})
	})

	// Tests for complex scenarios with multiple simultaneous actions
	// This test verifies handling of onboarding, trigger builds, create configuration, offboarding, and invalid versions happening concurrently
	Context("Complex multi-action scenarios", func() {
		var (
			namespace           = "multi-action-ns"
			componentKey        = types.NamespacedName{Name: "test-component", Namespace: namespace}
			buildRoleBindingKey = types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, pacHost)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()

			// Mock HTTP client for PaC webhook POST requests
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			gock.InterceptClient(client)
			GetHttpClientFunction = func() *http.Client {
				return client
			}
		})

		_ = AfterEach(func() {
			deleteComponentAndOwnedResources(componentKey)
			gock.Off()
		})

		// Tests the complete component lifecycle with GitHub App authentication:
		// - PHASE 1: Onboard v1 with CreateConfiguration PR, v2 without config; validates invalid version in action is ignored
		// - PHASE 2: Add v3 with CreateConfiguration PR, add v4 without config, trigger v2; validates invalid version in trigger is ignored
		// - PHASE 3: Offboard v4 (purge PR), onboard v5, trigger v1; validates purge PR creation and invalid version in trigger is ignored
		// - Validates: no webhook setup/deletion (GitHub App uses incoming webhooks), Repository CR, service account, finalizer
		It("should handle comprehensive lifecycle with githubApp: onboarding, create config, trigger, invalid versions, and offboarding", func() {
			version1Name := "v1"
			version2Name := "v2"
			version3Name := "v3"
			version4Name := "v4"
			version5Name := "v5"
			v1PrUrl := "https://githost.com/mr/1111"
			v3PrUrl := "https://githost.com/mr/3333"
			purgePrUrl := UndoPacMergeRequestURL

			// Track PR creation calls
			prCreatedForVersions := make(map[string]bool)
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				// Determine which version this PR is for based on branch name
				var versionName, baseBranch, returnUrl string
				switch d.BranchName {
				case "konflux-" + componentKey.Name + "-" + version1Name:
					versionName = version1Name
					baseBranch = "main"
					returnUrl = v1PrUrl
				case "konflux-" + componentKey.Name + "-" + version3Name:
					versionName = version3Name
					baseBranch = "feature"
					returnUrl = v3PrUrl
				default:
					Fail("Unexpected branch name: " + d.BranchName)
				}

				// Verify MergeRequestData fields
				gitURL := SampleRepoLink + "-" + componentKey.Name
				commitMessage := "git-app-name update " + componentKey.Name + ":" + versionName
				title := "git-app-name update " + componentKey.Name + ":" + versionName
				branchName := "konflux-" + componentKey.Name + "-" + versionName
				verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, baseBranch)

				// Each PR should have exactly 2 files: pull and push pipeline
				Expect(len(d.Files)).To(Equal(2))

				// Verify pipelines are present
				expectedPullPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPRSuffix
				expectedPushPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPushSuffix

				foundPullPipeline := false
				foundPushPipeline := false
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())

					var prYaml tektonapi.PipelineRun
					Expect(yaml.Unmarshal(file.Content, &prYaml)).To(Succeed())

					// When pipeline comes from bundle, spec is embedded inline
					Expect(prYaml.Spec.PipelineSpec).ToNot(BeNil(), "PipelineSpec should be embedded from bundle")
					Expect(prYaml.Spec.PipelineSpec.Tasks).ToNot(BeEmpty(), "Pipeline should have tasks")

					if prYaml.Name == expectedPullPipelineName {
						foundPullPipeline = true
					} else if prYaml.Name == expectedPushPipelineName {
						foundPushPipeline = true
					}
				}

				Expect(foundPullPipeline).To(BeTrue(), "PR should contain pull pipeline")
				Expect(foundPushPipeline).To(BeTrue(), "PR should contain push pipeline")
				prCreatedForVersions[versionName] = true

				return returnUrl, nil
			}

			// Track purge PR creation
			isPurgePRCreated := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				defer GinkgoRecover()

				// Verify purge PR is for v4
				// Only verify the first call - deleteComponentAndOwnedResources in AfterEach will also call this method
				if !isPurgePRCreated {
					expectedGitURL := SampleRepoLink + "-" + componentKey.Name
					commitMessage := "git-app-name purge " + componentKey.Name + ":" + version4Name
					title := "git-app-name purge " + componentKey.Name + ":" + version4Name
					branchName := "konflux-purge-" + componentKey.Name + "-" + version4Name
					baseBranch := "release"
					verifyMergeRequestData(repoUrl, d, expectedGitURL, commitMessage, title, branchName, baseBranch)

					// Verify purge PR data contains files to delete
					Expect(len(d.Files)).To(Equal(2), "Should delete both pull and push pipeline files")
					for _, file := range d.Files {
						Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue(), "File should be in .tekton directory")
						// Files should be empty for deletion
						Expect(file.Content).To(BeNil())
					}
					isPurgePRCreated = true
				}

				return purgePrUrl, nil
			}

			// Track trigger build POST requests
			var capturedPostData []map[string]interface{}
			webhookTargetUrl := "https://" + pacHost

			gock.New(webhookTargetUrl).
				Post("/incoming").
				Persist(). // Allow multiple calls - exact count verified later in test
				SetMatcher(gock.NewBasicMatcher()).
				AddMatcher(gock.MatchFunc(func(req *http.Request, _ *gock.Request) (bool, error) {
					defer GinkgoRecover()
					var bodyJson map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&bodyJson); err != nil {
						return false, err
					}
					capturedPostData = append(capturedPostData, bodyJson)
					return true, nil
				})).
				Reply(202).JSON(map[string]interface{}{})

			// Verify SetupPaCWebhookFunc is NOT called when using GitHub App
			SetupPaCWebhookFunc = func(string, string, string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			// Verify DeletePaCWebhookFunc is NOT called when offboarding version (only component deletion)
			DeletePaCWebhookFunc = func(string, string) error {
				defer GinkgoRecover()
				Fail("Should not try to delete webhook when offboarding version with GitHub application")
				return nil
			}

			// PHASE 1: Create component with v1 and v2 for onboarding
			// CreateConfiguration includes v1 (valid) and "nonexistent-v1" (invalid)
			// v2 will onboard without config
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						Versions: []string{version1Name, "nonexistent-v1"},
					},
				},
			})
			createComponent(component)

			// Wait for both versions to be onboarded
			waitForComponentStatusVersions(componentKey, 2)

			// Verify v1 PR was created (invalid version should be ignored)
			Eventually(func() bool {
				return prCreatedForVersions[version1Name]
			}, timeout, interval).Should(BeTrue())

			// CreateConfiguration action should be completely cleared (both valid and invalid versions)
			component = waitForComponentSpecActionEmpty(componentKey, "create-pr", "versions")

			// Verify both versions in status
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}
			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")

			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			// Verify service account was created
			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			// Verify role binding exists and service account is in it
			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))

			// Verify Repository CR was created with correct URL and no GitProvider (GitHub App)
			validatePaCRepository(component, "", "")

			// PHASE 2: Add v3 with CreateConfiguration, add v4 without config, trigger v2 with invalid version
			component = getComponent(componentKey)
			component.Spec.Source.Versions = append(component.Spec.Source.Versions,
				compapiv1alpha1.ComponentVersion{Name: version3Name, Revision: "feature"},
				compapiv1alpha1.ComponentVersion{Name: version4Name, Revision: "release"},
			)
			component.Spec.Actions.CreateConfiguration = compapiv1alpha1.ComponentCreatePipelineConfiguration{
				Versions: []string{version3Name},
			}
			component.Spec.Actions.TriggerBuilds = []string{version2Name, "nonexistent-v2"}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for all versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 4)

			// Verify v3 PR was created
			Eventually(func() bool {
				return prCreatedForVersions[version3Name]
			}, timeout, interval).Should(BeTrue())

			// Both actions should be cleared (invalid versions should be removed)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Spec.Actions.CreateConfiguration.Versions) == 0 &&
					len(component.Spec.Actions.TriggerBuilds) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify Repository CR was created and get repository name
			repository := validatePaCRepository(component, "", "")
			repositoryName := repository.Name

			// Wait for incoming secret to be created (after trigger)
			incomingSecretName := getPaCIncomingSecretName(repositoryName)
			incomingSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretKey)

			var incomingSecret corev1.Secret
			Expect(k8sClient.Get(ctx, incomingSecretKey, &incomingSecret)).To(Succeed())
			secretValue := string(incomingSecret.Data[pacIncomingSecretKey])

			// Verify HTTP POST request was made for v2 (invalid version should be ignored)
			Eventually(func() int {
				return len(capturedPostData)
			}, timeout, interval).Should(Equal(1))

			// Find v2 POST data
			var v2Data map[string]interface{}
			v2PipelineRunName := component.Name + "-" + version2Name + pipelineRunOnPushSuffix
			for _, data := range capturedPostData {
				if pipelinerun, ok := data["pipelinerun"].(string); ok && pipelinerun == v2PipelineRunName {
					v2Data = data
					break
				}
			}
			Expect(v2Data).NotTo(BeNil(), "v2 POST request should exist")
			verifyPacWebhookIncomingPostData(v2Data, repositoryName, secretValue, v2PipelineRunName, namespace, "develop", component.Spec.Source.GitURL)

			// Verify all 4 versions in status
			versionMap = make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}
			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")
			Expect(versionMap).To(HaveKey(version3Name))
			verifyComponentVersionStatus(versionMap[version3Name], version3Name, "feature", "succeeded", v3PrUrl, "")
			Expect(versionMap).To(HaveKey(version4Name))
			verifyComponentVersionStatus(versionMap[version4Name], version4Name, "release", "succeeded", "", "")

			// Verify Repository.Spec.Incomings was updated with v2's revision
			repository = validatePaCRepository(component, "", "")
			validatePaCRepositoryIncomings(repository, []string{"develop"})

			// PHASE 3: Offboard v4, onboard v5 without config, trigger v1 with invalid version
			component = getComponent(componentKey)
			component.Spec.Source.Versions = []compapiv1alpha1.ComponentVersion{
				{Name: version1Name, Revision: "main"},
				{Name: version2Name, Revision: "develop"},
				{Name: version3Name, Revision: "feature"},
				{Name: version5Name, Revision: "staging"},
			}
			component.Spec.Actions.TriggerBuilds = []string{version1Name, "nonexistent-v3"}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for purge PR to be created
			Eventually(func() bool {
				return isPurgePRCreated
			}, timeout, interval).Should(BeTrue())

			// Wait for v4 to be removed and v5 to be onboarded
			Eventually(func() bool {
				component = getComponent(componentKey)
				if len(component.Status.Versions) != 4 {
					return false
				}
				for _, v := range component.Status.Versions {
					if v.Name == version4Name {
						return false
					}
				}
				return true
			}, timeout, interval).Should(BeTrue())

			// TriggerBuilds action should be cleared (invalid version should be removed)
			component = waitForComponentSpecActionEmpty(componentKey, "trigger", "builds")

			// Verify v1 trigger happened (v2 was triggered in PHASE 2, now v1 in PHASE 3)
			Eventually(func() int {
				return len(capturedPostData)
			}, timeout, interval).Should(Equal(2))

			// Find v1 POST data
			var v1Data map[string]interface{}
			v1PipelineRunName := component.Name + "-" + version1Name + pipelineRunOnPushSuffix
			for _, data := range capturedPostData {
				if pipelinerun, ok := data["pipelinerun"].(string); ok && pipelinerun == v1PipelineRunName {
					v1Data = data
					break
				}
			}
			Expect(v1Data).NotTo(BeNil(), "v1 POST request should exist")
			verifyPacWebhookIncomingPostData(v1Data, repositoryName, secretValue, v1PipelineRunName, namespace, "main", component.Spec.Source.GitURL)

			// Verify all 4 versions in status (v1, v2, v3, v5)
			versionMap = make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}
			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")
			Expect(versionMap).To(HaveKey(version3Name))
			verifyComponentVersionStatus(versionMap[version3Name], version3Name, "feature", "succeeded", v3PrUrl, "")
			Expect(versionMap).To(HaveKey(version5Name))
			verifyComponentVersionStatus(versionMap[version5Name], version5Name, "staging", "succeeded", "", "")

			// Verify Repository.Spec.Incomings was updated with v1's revision
			repository = validatePaCRepository(component, "", "")
			validatePaCRepositoryIncomings(repository, []string{"develop", "main"})

			// Verify service account still exists (not removed when offboarding version)
			saKey = getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)

			// Verify finalizer is still present
			component = getComponent(componentKey)
			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))
		})

		// Tests the complete component lifecycle with GitLab token authentication:
		// - PHASE 1: Onboard v1 with CreateConfiguration PR, v2 without config; validates webhook setup and invalid version in action is ignored
		// - PHASE 2: Add v3 with CreateConfiguration PR, add v4 without config, trigger v2; validates invalid version in trigger is ignored
		// - PHASE 3: Offboard v4 (purge PR), onboard v5, trigger v1; validates purge PR and no webhook deletion during version offboarding
		// - Key difference from GitHub App: webhook is set up once on component creation, not deleted when offboarding individual versions
		It("should handle comprehensive lifecycle with GitLab token auth: onboarding, create config, trigger, invalid versions, offboarding, and webhook management", func() {
			version1Name := "v1"
			version2Name := "v2"
			version3Name := "v3"
			version4Name := "v4"
			version5Name := "v5"
			v1PrUrl := "https://githost.com/mr/1111"
			v3PrUrl := "https://githost.com/mr/3333"
			purgePrUrl := UndoPacMergeRequestURL
			gitURL := "https://gitlab.com/test-org/test-repo"

			// Create SCM secret for GitLab token authentication
			scmSecretKey := types.NamespacedName{Name: "gitlab-token-secret", Namespace: namespace}
			scmSecretData := map[string]string{
				"password": "test-token",
			}
			labels := map[string]string{
				"appstudio.redhat.com/credentials": "scm",
				"appstudio.redhat.com/scm.host":    "gitlab.com",
			}
			createSCMSecret(scmSecretKey, scmSecretData, corev1.SecretTypeBasicAuth, nil, labels)
			defer deleteSecret(scmSecretKey)

			// Track webhook setup calls
			webhookSetupCount := 0
			SetupPaCWebhookFunc = func(repoUrl, webhookTargetUrl, webhookSecret string) error {
				defer GinkgoRecover()
				webhookSetupCount++
				Expect(repoUrl).To(Equal(gitURL))
				Expect(webhookTargetUrl).To(Equal("https://" + pacHost))
				Expect(webhookSecret).ToNot(BeEmpty())
				return nil
			}

			// Track webhook deletion calls - should NOT be called when offboarding version
			webhookDeleteCalled := false
			DeletePaCWebhookFunc = func(repoUrl, webhookUrl string) error {
				defer GinkgoRecover()
				webhookDeleteCalled = true
				Fail("DeletePaCWebhook should not be called when offboarding version, only when deleting component")
				return nil
			}

			// Track PR creation calls
			prCreatedForVersions := make(map[string]bool)
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				// Determine which version this PR is for based on branch name
				var versionName, baseBranch, returnUrl string
				switch d.BranchName {
				case "konflux-" + componentKey.Name + "-" + version1Name:
					versionName = version1Name
					baseBranch = "main"
					returnUrl = v1PrUrl
				case "konflux-" + componentKey.Name + "-" + version3Name:
					versionName = version3Name
					baseBranch = "feature"
					returnUrl = v3PrUrl
				default:
					Fail("Unexpected branch name: " + d.BranchName)
				}

				// Verify MergeRequestData fields
				commitMessage := "Konflux update " + componentKey.Name + ":" + versionName
				title := "Konflux update " + componentKey.Name + ":" + versionName
				branchName := "konflux-" + componentKey.Name + "-" + versionName
				verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, baseBranch)

				// Each PR should have exactly 2 files: pull and push pipeline
				Expect(len(d.Files)).To(Equal(2))

				// Verify pipelines are present
				expectedPullPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPRSuffix
				expectedPushPipelineName := componentKey.Name + "-" + versionName + pipelineRunOnPushSuffix

				foundPullPipeline := false
				foundPushPipeline := false
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())

					var prYaml tektonapi.PipelineRun
					Expect(yaml.Unmarshal(file.Content, &prYaml)).To(Succeed())

					// When pipeline comes from bundle, spec is embedded inline
					Expect(prYaml.Spec.PipelineSpec).ToNot(BeNil(), "PipelineSpec should be embedded from bundle")
					Expect(prYaml.Spec.PipelineSpec.Tasks).ToNot(BeEmpty(), "Pipeline should have tasks")

					if prYaml.Name == expectedPullPipelineName {
						foundPullPipeline = true
					} else if prYaml.Name == expectedPushPipelineName {
						foundPushPipeline = true
					}
				}

				Expect(foundPullPipeline).To(BeTrue(), "PR should contain pull pipeline")
				Expect(foundPushPipeline).To(BeTrue(), "PR should contain push pipeline")
				prCreatedForVersions[versionName] = true

				return returnUrl, nil
			}

			// Track purge PR creation
			isPurgePRCreated := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				defer GinkgoRecover()

				// Verify purge PR is for v4
				// Only verify the first call - deleteComponentAndOwnedResources in AfterEach will also call this method
				if !isPurgePRCreated {
					commitMessage := "Konflux purge " + componentKey.Name + ":" + version4Name
					title := "Konflux purge " + componentKey.Name + ":" + version4Name
					branchName := "konflux-purge-" + componentKey.Name + "-" + version4Name
					baseBranch := "release"
					verifyMergeRequestData(repoUrl, d, gitURL, commitMessage, title, branchName, baseBranch)

					// Verify purge PR data contains files to delete
					Expect(len(d.Files)).To(Equal(2), "Should delete both pull and push pipeline files")
					for _, file := range d.Files {
						Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue(), "File should be in .tekton directory")
						// Files should be empty for deletion
						Expect(file.Content).To(BeNil())
					}
					isPurgePRCreated = true
				}

				return purgePrUrl, nil
			}

			// Track trigger build POST requests
			var capturedPostData []map[string]interface{}
			webhookTargetUrl := "https://" + pacHost

			gock.New(webhookTargetUrl).
				Post("/incoming").
				Persist(). // Allow multiple calls - exact count verified later in test
				SetMatcher(gock.NewBasicMatcher()).
				AddMatcher(gock.MatchFunc(func(req *http.Request, _ *gock.Request) (bool, error) {
					defer GinkgoRecover()
					var bodyJson map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&bodyJson); err != nil {
						return false, err
					}
					capturedPostData = append(capturedPostData, bodyJson)
					return true, nil
				})).
				Reply(202).JSON(map[string]interface{}{})

			// PHASE 1: Create component with v1 and v2 for onboarding
			// CreateConfiguration includes v1 (valid) and "nonexistent-v1" (invalid)
			// v2 will onboard without config
			component := getComponentData(componentConfig{
				componentKey: componentKey,
				gitURL:       gitURL,
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: version1Name, Revision: "main"},
					{Name: version2Name, Revision: "develop"},
				},
				actions: compapiv1alpha1.ComponentActions{
					CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{
						Versions: []string{version1Name, "nonexistent-v1"},
					},
				},
			})
			component = createComponent(component)

			// Verify webhook setup was called once (for component creation)
			Eventually(func() int {
				return webhookSetupCount
			}, timeout, interval).Should(Equal(1), "SetupPaCWebhook should be called once for token-based auth")

			webhookSecretKey := types.NamespacedName{Name: pipelinesAsCodeWebhooksSecretName, Namespace: namespace}

			// Verify webhook secret was created
			Eventually(func() bool {
				webhookSecret := &corev1.Secret{}
				if err := k8sClient.Get(ctx, webhookSecretKey, webhookSecret); err != nil {
					return false
				}
				expectedKey := getWebhookSecretKeyForComponent(*component, true)
				return len(webhookSecret.Data[expectedKey]) > 0
			}, timeout, interval).Should(BeTrue())

			// Wait for both versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 2)

			// Verify v1 PR was created (invalid version should be ignored)
			Eventually(func() bool {
				return prCreatedForVersions[version1Name]
			}, timeout, interval).Should(BeTrue())

			// CreateConfiguration action should be completely cleared (both valid and invalid versions)
			component = waitForComponentSpecActionEmpty(componentKey, "create-pr", "versions")

			// Verify both versions in status
			versionMap := make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}
			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")

			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			// Verify service account was created
			saKey := getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			// Verify role binding exists and service account is in it
			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))

			// Verify Repository CR was created with correct URL and GitProvider (token auth)
			validatePaCRepository(component, scmSecretKey.Name, "password")

			// PHASE 2: Add v3 with CreateConfiguration, add v4 without config, trigger v2 with invalid version
			component = getComponent(componentKey)
			component.Spec.Source.Versions = append(component.Spec.Source.Versions,
				compapiv1alpha1.ComponentVersion{Name: version3Name, Revision: "feature"},
				compapiv1alpha1.ComponentVersion{Name: version4Name, Revision: "release"},
			)
			component.Spec.Actions.CreateConfiguration = compapiv1alpha1.ComponentCreatePipelineConfiguration{
				Versions: []string{version3Name},
			}
			component.Spec.Actions.TriggerBuilds = []string{version2Name, "nonexistent-v2"}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for all versions to be onboarded
			component = waitForComponentStatusVersions(componentKey, 4)

			// Verify v3 PR was created
			Eventually(func() bool {
				return prCreatedForVersions[version3Name]
			}, timeout, interval).Should(BeTrue())

			// Both actions should be cleared (invalid versions should be removed)
			Eventually(func() bool {
				component = getComponent(componentKey)
				return len(component.Spec.Actions.CreateConfiguration.Versions) == 0 &&
					len(component.Spec.Actions.TriggerBuilds) == 0
			}, timeout, interval).Should(BeTrue())

			// Verify Repository CR was created and get repository name
			repository := validatePaCRepository(component, scmSecretKey.Name, "password")
			repositoryName := repository.Name

			// Wait for incoming secret to be created (after trigger)
			incomingSecretName := getPaCIncomingSecretName(repositoryName)
			incomingSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretKey)

			var incomingSecret corev1.Secret
			Expect(k8sClient.Get(ctx, incomingSecretKey, &incomingSecret)).To(Succeed())
			secretValue := string(incomingSecret.Data[pacIncomingSecretKey])

			// Verify HTTP POST request was made for v2 (invalid version should be ignored)
			Eventually(func() int {
				return len(capturedPostData)
			}, timeout, interval).Should(Equal(1))

			// Find v2 POST data
			var v2Data map[string]interface{}
			v2PipelineRunName := component.Name + "-" + version2Name + pipelineRunOnPushSuffix
			for _, data := range capturedPostData {
				if pipelinerun, ok := data["pipelinerun"].(string); ok && pipelinerun == v2PipelineRunName {
					v2Data = data
					break
				}
			}
			Expect(v2Data).NotTo(BeNil(), "v2 POST request should exist")
			verifyPacWebhookIncomingPostData(v2Data, repositoryName, secretValue, v2PipelineRunName, namespace, "develop", component.Spec.Source.GitURL)

			// Verify all 4 versions in status
			versionMap = make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}
			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")
			Expect(versionMap).To(HaveKey(version3Name))
			verifyComponentVersionStatus(versionMap[version3Name], version3Name, "feature", "succeeded", v3PrUrl, "")
			Expect(versionMap).To(HaveKey(version4Name))
			verifyComponentVersionStatus(versionMap[version4Name], version4Name, "release", "succeeded", "", "")

			// Verify Repository.Spec.Incomings was updated with v2's revision
			repository = validatePaCRepository(component, scmSecretKey.Name, "password")
			validatePaCRepositoryIncomings(repository, []string{"develop"})

			// Verify webhook setup was called for each phase with onboarding/config creation
			// Called during: PHASE 1 (v1/v2 onboarding), PHASE 2 (v3/v4 onboarding)
			// Multiple reconciles may occur, so expect at least 2
			Expect(webhookSetupCount).To(BeNumerically(">=", 2), "SetupPaCWebhook called for each onboarding phase")

			// PHASE 3: Offboard v4, onboard v5 without config, trigger v1 with invalid version
			// Webhook should NOT be deleted when offboarding a version
			component = getComponent(componentKey)
			component.Spec.Source.Versions = []compapiv1alpha1.ComponentVersion{
				{Name: version1Name, Revision: "main"},
				{Name: version2Name, Revision: "develop"},
				{Name: version3Name, Revision: "feature"},
				{Name: version5Name, Revision: "staging"},
			}
			component.Spec.Actions.TriggerBuilds = []string{version1Name, "nonexistent-v3"}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Wait for purge PR to be created
			Eventually(func() bool {
				return isPurgePRCreated
			}, timeout, interval).Should(BeTrue())

			// Wait for v4 to be removed and v5 to be onboarded
			Eventually(func() bool {
				component = getComponent(componentKey)
				if len(component.Status.Versions) != 4 {
					return false
				}
				for _, v := range component.Status.Versions {
					if v.Name == version4Name {
						return false
					}
				}
				return true
			}, timeout, interval).Should(BeTrue())

			// TriggerBuilds action should be cleared (invalid version should be removed)
			component = waitForComponentSpecActionEmpty(componentKey, "trigger", "builds")

			// Verify v1 trigger happened (v2 was triggered in PHASE 2, now v1 in PHASE 3)
			Eventually(func() int {
				return len(capturedPostData)
			}, timeout, interval).Should(Equal(2))

			// Find v1 POST data
			var v1Data map[string]interface{}
			v1PipelineRunName := component.Name + "-" + version1Name + pipelineRunOnPushSuffix
			for _, data := range capturedPostData {
				if pipelinerun, ok := data["pipelinerun"].(string); ok && pipelinerun == v1PipelineRunName {
					v1Data = data
					break
				}
			}
			Expect(v1Data).NotTo(BeNil(), "v1 POST request should exist")
			verifyPacWebhookIncomingPostData(v1Data, repositoryName, secretValue, v1PipelineRunName, namespace, "main", component.Spec.Source.GitURL)

			// Verify all 4 versions in status (v1, v2, v3, v5)
			versionMap = make(map[string]compapiv1alpha1.ComponentVersionStatus)
			for _, v := range component.Status.Versions {
				versionMap[v.Name] = v
			}
			Expect(versionMap).To(HaveKey(version1Name))
			verifyComponentVersionStatus(versionMap[version1Name], version1Name, "main", "succeeded", v1PrUrl, "")
			Expect(versionMap).To(HaveKey(version2Name))
			verifyComponentVersionStatus(versionMap[version2Name], version2Name, "develop", "succeeded", "", "")
			Expect(versionMap).To(HaveKey(version3Name))
			verifyComponentVersionStatus(versionMap[version3Name], version3Name, "feature", "succeeded", v3PrUrl, "")
			Expect(versionMap).To(HaveKey(version5Name))
			verifyComponentVersionStatus(versionMap[version5Name], version5Name, "staging", "succeeded", "", "")

			// Verify Repository.Spec.Incomings was updated with v1's revision
			repository = validatePaCRepository(component, scmSecretKey.Name, "password")
			validatePaCRepositoryIncomings(repository, []string{"develop", "main"})

			// Verify webhook setup was called again for v5 onboarding
			// PHASE 1: v1/v2 onboarding, PHASE 2: v3/v4 onboarding, PHASE 3: v5 onboarding
			// Multiple reconciles may occur, so expect at least 3
			Expect(webhookSetupCount).To(BeNumerically(">=", 3), "SetupPaCWebhook called for each onboarding phase including v5")

			// Verify service account still exists (not removed when offboarding version)
			saKey = getComponentServiceAccountKey(componentKey)
			waitServiceAccount(saKey)

			waitServiceAccountInRoleBinding(buildRoleBindingKey, saKey.Name)

			// Verify finalizer is still present
			component = getComponent(componentKey)
			Expect(component.Finalizers).To(ContainElement(PaCProvisionFinalizer))

			// Verify webhook was NOT deleted when offboarding version
			Expect(webhookDeleteCalled).To(BeFalse(), "DeletePaCWebhook should NOT be called when offboarding version")

			// PHASE 4: Delete entire component - webhook should be deleted
			// Update DeletePaCWebhookFunc to allow deletion
			isDeletePaCWebhookCalled := false
			DeletePaCWebhookFunc = func(repoUrl string, webhookUrl string) error {
				defer GinkgoRecover()
				isDeletePaCWebhookCalled = true
				Expect(repoUrl).To(Equal(gitURL))
				Expect(webhookUrl).To(Equal("https://" + pacHost))
				return nil
			}

			deleteComponent(componentKey)

			// DeletePaCWebhook should be called when deleting component with token auth
			Eventually(func() bool {
				return isDeletePaCWebhookCalled
			}, timeout, interval).Should(BeTrue())
		})
	})
})
