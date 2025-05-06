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
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"

	"github.com/h2non/gock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	"github.com/konflux-ci/build-service/pkg/boerrors"
	. "github.com/konflux-ci/build-service/pkg/common"
	"github.com/konflux-ci/build-service/pkg/git/github"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
	gpf "github.com/konflux-ci/build-service/pkg/git/gitproviderfactory"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	//+kubebuilder:scaffold:imports
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

var (
	defaultPipelineAnnotationValue = fmt.Sprintf("{\"name\":\"%s\",\"bundle\":\"%s\"}", defaultPipelineName, defaultPipelineBundle)
	defaultPipelineAnnotations     = map[string]string{defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue}
)

var _ = Describe("Component build controller", func() {

	const (
		pacHost       = "pac-host"
		pacWebhookUrl = "https://" + pacHost
	)

	var (
		// All related to the component resources have the same key (but different type)
		pacRouteKey  = types.NamespacedName{Name: pipelinesAsCodeRouteName, Namespace: pipelinesAsCodeNamespace}
		pacSecretKey = types.NamespacedName{Name: PipelinesAsCodeGitHubAppSecretName, Namespace: BuildServiceNamespaceName}
	)

	BeforeEach(func() {
		createNamespace(BuildServiceNamespaceName)
		github.GetAllAppInstallations = func(githubAppIdStr string, appPrivateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
			return nil, "slug", nil
		}
	})

	Context("Test build pipeline Service Account", func() {
		var (
			namespace           = "sa-test"
			component1Key       = types.NamespacedName{Name: "component-sa-1", Namespace: namespace}
			component2Key       = types.NamespacedName{Name: "component-sa-2", Namespace: namespace}
			buildRoleBindingKey = types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			createNamespace(BuildServiceNamespaceName)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponent(component1Key)
			deleteComponent(component2Key)

			deleteRoleBinding(buildRoleBindingKey)
			deleteServiceAccount(getComponentServiceAccountKey(component1Key))
			deleteServiceAccount(getComponentServiceAccountKey(component2Key))
		})

		It("should create build pipeline dedicated service account and role binding", func() {
			createComponent(component1Key)
			component1SAKey := getComponentServiceAccountKey(component1Key)
			waitServiceAccount(component1SAKey)
			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(1))
		})

		It("should create build pipeline dedicated service account for each component and common role binding", func() {
			createComponent(component1Key)
			component1SAKey := getComponentServiceAccountKey(component1Key)
			waitServiceAccount(component1SAKey)
			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(1))

			createComponent(component2Key)
			component2SAKey := getComponentServiceAccountKey(component2Key)
			waitServiceAccount(component2SAKey)
			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(2))
		})

		It("should remove build pipeline dedicated service account for each component and common role binding when the last service account removed", func() {
			component1Config := getComponentData(componentConfig{componentKey: component1Key})
			// Simulate PaC provision was done
			component1Config.Finalizers = append(component1Config.Finalizers, PaCProvisionFinalizer)
			Expect(k8sClient.Create(ctx, component1Config)).To(Succeed())

			component1SAKey := getComponentServiceAccountKey(component1Key)
			component1SA := waitServiceAccount(component1SAKey)
			Expect(component1SA.OwnerReferences).To(HaveLen(1))
			Expect(component1SA.OwnerReferences[0].Name).To(Equal(component1Key.Name))
			Expect(component1SA.OwnerReferences[0].Kind).To(Equal("Component"))

			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))

			component2Config := getComponentData(componentConfig{componentKey: component2Key})
			// Simulate PaC provision was done
			component2Config.Finalizers = append(component1Config.Finalizers, PaCProvisionFinalizer)
			Expect(k8sClient.Create(ctx, component2Config)).To(Succeed())

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

		It("should clean up dangling role binding subjects and delete role binding", func() {
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey: component1Key,
				finalizers:   []string{PaCProvisionFinalizer}, // simulate PaC provision was done
			})

			component1SAKey := getComponentServiceAccountKey(component1Key)
			component1SA := waitServiceAccount(component1SAKey)
			Expect(component1SA.OwnerReferences).To(HaveLen(1))
			Expect(component1SA.OwnerReferences[0].Name).To(Equal(component1Key.Name))
			Expect(component1SA.OwnerReferences[0].Kind).To(Equal("Component"))

			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(1))

			// Create another component without successful PaC provison, so the finalizer won't exist.
			createComponent(component2Key)
			component2SAKey := getComponentServiceAccountKey(component2Key)
			component2SA := waitServiceAccount(component2SAKey)
			Expect(component2SA.OwnerReferences).To(HaveLen(1))
			Expect(component2SA.OwnerReferences[0].Name).To(Equal(component2Key.Name))
			Expect(component2SA.OwnerReferences[0].Kind).To(Equal("Component"))

			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(2))

			deleteComponent(component2Key)
			// Simulate owner reference is working
			deleteServiceAccount(component2SAKey)

			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			// We've got dangling subject in the role binding, it's expected To(HaveLen(1)),
			// but because finalizer is was not added, it doesn't reach the clean up point.
			Expect(roleBinding.Subjects).To(HaveLen(2))
			// Add another dangling subject
			roleBinding.Subjects = append(roleBinding.Subjects, rbacv1.Subject{
				Kind: "ServiceAccount",
				Name: "unrelated",
			})
			Expect(k8sClient.Update(ctx, &roleBinding)).To(Succeed())
			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.Subjects).To(HaveLen(3))

			// Upon deletion of a provisioned Component, all dangling references should be cleaned up
			deleteComponent(component1Key)
			// Since no subjects left, the role binding should be deleted too
			waitRoleBindingGone(buildRoleBindingKey)
		})

		It("should clean up dangling role binding subjects and keep role binding", func() {
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey: component1Key,
				finalizers:   []string{PaCProvisionFinalizer}, // simulate PaC provision was done
			})

			component1SAKey := getComponentServiceAccountKey(component1Key)
			component1SA := waitServiceAccount(component1SAKey)
			Expect(component1SA.OwnerReferences).To(HaveLen(1))
			Expect(component1SA.OwnerReferences[0].Name).To(Equal(component1Key.Name))
			Expect(component1SA.OwnerReferences[0].Kind).To(Equal("Component"))

			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(1))

			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey: component2Key,
				finalizers:   []string{PaCProvisionFinalizer}, // simulate PaC provision was done
			})

			component2SAKey := getComponentServiceAccountKey(component2Key)
			component2SA := waitServiceAccount(component2SAKey)
			Expect(component2SA.OwnerReferences).To(HaveLen(1))
			Expect(component2SA.OwnerReferences[0].Name).To(Equal(component2Key.Name))
			Expect(component2SA.OwnerReferences[0].Kind).To(Equal("Component"))

			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(2))

			// Add a dangling subject to the Role Binding
			roleBinding.Subjects = append(roleBinding.Subjects, rbacv1.Subject{
				Kind: "ServiceAccount",
				Name: "dangling",
			})
			Expect(k8sClient.Update(ctx, &roleBinding)).To(Succeed())

			deleteComponent(component2Key)
			// Simulate owner reference is working
			deleteServiceAccount(component2SAKey)

			// Since Component 1 is still exist, the Role Binding should be kept
			// we will wait for SA disappear from binding, because component might be removed sooner (as finalizer is removed before unprovision and removal from rolebinding)
			// then removal from rolebinding happens
			roleBinding = waitServiceAccountNotInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.Subjects).To(HaveLen(1))
		})

		It("should restore build pipeline dedicated service account on reconcile", func() {
			createComponent(component1Key)
			component1SAKey := getComponentServiceAccountKey(component1Key)
			waitServiceAccount(component1SAKey)
			waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)

			deleteServiceAccount(component1SAKey)

			// trigger a reconcile
			component1 := getComponent(component1Key)
			component1.Annotations["test-change"] = "reconcile"
			Expect(k8sClient.Update(ctx, component1)).To(Succeed())

			waitServiceAccount(component1SAKey)
			waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
		})

		It("should restore build pipelines role binding on reconcile", func() {
			createComponent(component1Key)
			component1SAKey := getComponentServiceAccountKey(component1Key)
			waitServiceAccount(component1SAKey)
			roleBinding := waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(1))

			createComponent(component2Key)
			component2SAKey := getComponentServiceAccountKey(component2Key)
			waitServiceAccount(component2SAKey)
			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
			Expect(roleBinding.Subjects).To(HaveLen(2))

			deleteRoleBinding(buildRoleBindingKey)

			// trigger a reconcile for component 1
			component1 := getComponent(component1Key)
			component1.Annotations["test-change"] = "reconcile"
			Expect(k8sClient.Update(ctx, component1)).To(Succeed())
			// trigger a reconcile for component 2
			component2 := getComponent(component2Key)
			component2.Annotations["test-change"] = "reconcile"
			Expect(k8sClient.Update(ctx, component2)).To(Succeed())

			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component1SAKey.Name)
			roleBinding = waitServiceAccountInRoleBinding(buildRoleBindingKey, component2SAKey.Name)
			Expect(roleBinding.Subjects).To(HaveLen(2))
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(BuildPipelineClusterRoleName))
		})

		It("should create PaC PR with build pipeline service account", func() {
			// TODO remove the config map creation after migration is done
			useNewSaConfigMap := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "use-new-sa", Namespace: BuildServiceNamespaceName},
			}
			Expect(k8sClient.Create(ctx, &useNewSaConfigMap)).To(Succeed())

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					buildPipelineData := &tektonapi.PipelineRun{}
					Expect(yaml.Unmarshal(file.Content, buildPipelineData)).To(Succeed())
					Expect(buildPipelineData.Spec.TaskRunTemplate.ServiceAccountName).To(Equal(getComponentServiceAccountKey(component1Key).Name))
				}

				isCreatePaCPullRequestInvoked = true
				return "merge-url", nil
			}

			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: component1Key,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
		})

		It("should manage pull secrets for nudging", func() {
			nudgedComponentKey := types.NamespacedName{Namespace: namespace, Name: "nudged-component"}
			nudgedComponentSAKey := getComponentServiceAccountKey(nudgedComponentKey)

			createComponent(nudgedComponentKey)
			defer deleteComponent(nudgedComponentKey)
			nudgedComponentImageRepo := createImageRepositoryForComponent(nudgedComponentKey)
			defer deleteImageRepository(nudgedComponentKey)

			// Simulate Image Controller and add push secret of nudged Component to its Service Account
			nudgedComponentSA := waitServiceAccount(nudgedComponentSAKey)
			nudgedComponentSA.Secrets = append(nudgedComponentSA.Secrets, corev1.ObjectReference{Name: nudgedComponentImageRepo.Status.Credentials.PushSecretName})
			Expect(k8sClient.Update(ctx, &nudgedComponentSA)).To(Succeed())

			component1SAKey := getComponentServiceAccountKey(component1Key)
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey: component1Key,
				finalizers:   []string{PaCProvisionFinalizer}, // Simulate PaC provision done
			})
			component1ImageRepo := createImageRepositoryForComponent(component1Key)
			defer deleteImageRepository(component1Key)

			// Create a component with nudge reference
			component2SAKey := getComponentServiceAccountKey(component2Key)
			component2 := getSampleComponentData(component2Key)
			component2.Finalizers = append(component2.Finalizers, PaCProvisionFinalizer) // Simulate PaC provision done
			component2.Spec.BuildNudgesRef = []string{
				nudgedComponentKey.Name,
			}
			createComponentCustom(component2)
			component2ImageRepo := createImageRepositoryForComponent(component2Key)
			defer deleteImageRepository(component2Key)
			// Check that the pull secret of nudging Component was linked to the nudged Component build pipeline Service Account
			waitServiceAccountLinkedSecret(nudgedComponentSAKey, component2ImageRepo.Status.Credentials.PullSecretName, false)
			// Check that a pull secret was not linked from a not nudging Component
			waitServiceAccountLinkedSecretGone(nudgedComponentSAKey, component1ImageRepo.Status.Credentials.PullSecretName, false)
			// Also ensure, that the secret wasn't linked to a wrong Service Account
			waitServiceAccountLinkedSecretGone(component1SAKey, component2ImageRepo.Status.Credentials.PullSecretName, false)
			waitServiceAccountLinkedSecretGone(component2SAKey, component2ImageRepo.Status.Credentials.PullSecretName, false)

			// Add a nudge from Component 1 after its creation
			component1 := getComponent(component1Key)
			component1.Spec.BuildNudgesRef = []string{
				nudgedComponentKey.Name,
			}
			Expect(k8sClient.Update(ctx, component1)).To(Succeed())

			// Trigger a reconcile for nudged Component.
			// In real environment it should be done by update of BuildNudgedBy field in the nudged Component status
			nudgedComponent := getComponent(nudgedComponentKey)
			nudgedComponent.Annotations["build-nudged-by"] = "component1"
			Expect(k8sClient.Update(ctx, nudgedComponent)).To(Succeed())

			// Check that the pull secret of newly added nudging Component was linked to the nudged Component build pipeline Service Account
			waitServiceAccountLinkedSecret(nudgedComponentSAKey, component1ImageRepo.Status.Credentials.PullSecretName, false)
			// Check that the pull secret of earlier added nudging Component is kept linked to the nudged Component build pipeline Service Account
			waitServiceAccountLinkedSecret(nudgedComponentSAKey, component2ImageRepo.Status.Credentials.PullSecretName, false)

			// Also ensure, that the secrets waren't linked to a wrong Service Account
			waitServiceAccountLinkedSecretGone(component1SAKey, component1ImageRepo.Status.Credentials.PullSecretName, false)
			waitServiceAccountLinkedSecretGone(component1SAKey, component2ImageRepo.Status.Credentials.PullSecretName, false)
			waitServiceAccountLinkedSecretGone(component2SAKey, component1ImageRepo.Status.Credentials.PullSecretName, false)
			waitServiceAccountLinkedSecretGone(component2SAKey, component2ImageRepo.Status.Credentials.PullSecretName, false)

			// Delete a nudging Component and check unlinking
			deleteComponent(component2Key)
			waitServiceAccountLinkedSecretGone(nudgedComponentSAKey, component2ImageRepo.Status.Credentials.PullSecretName, false)
			// Check that the pull secret from still nudging Component is preserved
			waitServiceAccountLinkedSecret(nudgedComponentSAKey, component1ImageRepo.Status.Credentials.PullSecretName, false)

			deleteComponent(component1Key)
			waitServiceAccountLinkedSecretGone(nudgedComponentSAKey, component1ImageRepo.Status.Credentials.PullSecretName, false)

			// Ensure that the push secret of the nudged Component is not removed
			waitServiceAccountLinkedSecret(nudgedComponentSAKey, nudgedComponentImageRepo.Status.Credentials.PushSecretName, false)
		})
	})

	Context("Test Pipelines as Code build preparation", func() {
		var (
			namespace             = "pac-preparation"
			resourcePacPrepKey    = types.NamespacedName{Name: HASCompName + "-pacprepannotation", Namespace: namespace}
			namespacePaCSecretKey = types.NamespacedName{Name: PipelinesAsCodeGitHubAppSecretName, Namespace: namespace}
			webhookSecretKey      = types.NamespacedName{Name: pipelinesAsCodeWebhooksSecretName, Namespace: namespace}
			buildRoleBindingKey   = types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace}
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
			createRoute(pacRouteKey, "pac-host")
			createNamespace(BuildServiceNamespaceName)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponent(resourcePacPrepKey)
			deleteRoleBinding(buildRoleBindingKey)
			deleteServiceAccount(getComponentServiceAccountKey(resourcePacPrepKey))
			deletePaCRepository(resourcePacPrepKey)

			deleteSecret(webhookSecretKey)
			deleteSecret(namespacePaCSecretKey)

			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
		})

		It("should successfully submit PR with PaC definitions using GitHub application, without any request (defaults to pac provision)", func() {
			mergeUrl := "merge-url"

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourcePacPrepKey.Name))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.BranchName).ToNot(BeEmpty())
				Expect(d.BaseBranchName).To(Equal("main"))
				Expect(d.Title).ToNot(BeEmpty())
				Expect(d.Text).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return mergeUrl, nil
			}
			SetupPaCWebhookFunc = func(string, string, string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  defaultPipelineAnnotations,
			})

			pacRepo := waitPaCRepositoryCreated(resourcePacPrepKey)
			Expect(pacRepo.Spec.Params).ShouldNot(BeNil())
			existingParams := map[string]string{}
			for _, param := range *pacRepo.Spec.Params {
				existingParams[param.Name] = param.Value
			}
			val, ok := existingParams[pacCustomParamAppstudioWorkspace]
			Expect(ok).Should(BeTrue())
			Expect(val).Should(Equal("build"))

			waitPaCFinalizerOnComponent(resourcePacPrepKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			expectPacBuildStatus(resourcePacPrepKey, "enabled", 0, "", mergeUrl)
		})

		It("should successfully submit PR with PaC definitions using GitHub application", func() {
			mergeUrl := "merge-url"

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourcePacPrepKey.Name))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.BranchName).ToNot(BeEmpty())
				Expect(d.BaseBranchName).To(Equal("main"))
				Expect(d.Title).ToNot(BeEmpty())
				Expect(d.Text).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return mergeUrl, nil
			}
			SetupPaCWebhookFunc = func(string, string, string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			pacRepo := waitPaCRepositoryCreated(resourcePacPrepKey)
			Expect(pacRepo.Spec.Params).ShouldNot(BeNil())
			existingParams := map[string]string{}
			for _, param := range *pacRepo.Spec.Params {
				existingParams[param.Name] = param.Value
			}
			val, ok := existingParams[pacCustomParamAppstudioWorkspace]
			Expect(ok).Should(BeTrue())
			Expect(val).Should(Equal("build"))

			waitPaCFinalizerOnComponent(resourcePacPrepKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			expectPacBuildStatus(resourcePacPrepKey, "enabled", 0, "", mergeUrl)
		})

		It("should successfully submit PR with PaC definitions using GitHub application, even when pipeline annotation is missing, will add default one", func() {
			mergeUrl := "merge-url"

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourcePacPrepKey.Name))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.BranchName).ToNot(BeEmpty())
				Expect(d.BaseBranchName).To(Equal("main"))
				Expect(d.Title).ToNot(BeEmpty())
				Expect(d.Text).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return mergeUrl, nil
			}
			SetupPaCWebhookFunc = func(string, string, string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			annotations := map[string]string{}
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  annotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			pacRepo := waitPaCRepositoryCreated(resourcePacPrepKey)
			Expect(pacRepo.Spec.Params).ShouldNot(BeNil())
			existingParams := map[string]string{}
			for _, param := range *pacRepo.Spec.Params {
				existingParams[param.Name] = param.Value
			}
			val, ok := existingParams[pacCustomParamAppstudioWorkspace]
			Expect(ok).Should(BeTrue())
			Expect(val).Should(Equal("build"))

			waitPaCFinalizerOnComponent(resourcePacPrepKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			expectPacBuildStatus(resourcePacPrepKey, "enabled", 0, "", mergeUrl)
		})

		It("should successfully submit PR with PaC definitions using GitHub application, using 'latest' bundle", func() {
			mergeUrl := "merge-url"

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourcePacPrepKey.Name))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.BranchName).ToNot(BeEmpty())
				Expect(d.BaseBranchName).To(Equal("main"))
				Expect(d.Title).ToNot(BeEmpty())
				Expect(d.Text).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return mergeUrl, nil
			}
			SetupPaCWebhookFunc = func(string, string, string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			annotationValue := fmt.Sprintf("{\"name\":\"%s\",\"bundle\":\"%s\"}", defaultPipelineName, "latest")
			annotations := map[string]string{defaultBuildPipelineAnnotation: annotationValue}
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  annotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			pacRepo := waitPaCRepositoryCreated(resourcePacPrepKey)
			Expect(pacRepo.Spec.Params).ShouldNot(BeNil())
			existingParams := map[string]string{}
			for _, param := range *pacRepo.Spec.Params {
				existingParams[param.Name] = param.Value
			}
			val, ok := existingParams[pacCustomParamAppstudioWorkspace]
			Expect(ok).Should(BeTrue())
			Expect(val).Should(Equal("build"))

			waitPaCFinalizerOnComponent(resourcePacPrepKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			expectPacBuildStatus(resourcePacPrepKey, "enabled", 0, "", mergeUrl)
		})

		It("should fail to submit PR if build pipeline annotation isn't valid json", func() {
			annotations := map[string]string{defaultBuildPipelineAnnotation: "wrong"}
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  annotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(resourcePacPrepKey, BuildRequestAnnotationName)

			expectError := boerrors.NewBuildOpError(boerrors.EFailedToParsePipelineAnnotation, nil)

			buildStatus := readBuildStatus(getComponent(resourcePacPrepKey))
			Expect(buildStatus).ToNot(BeNil())
			errorMessage := fmt.Sprintf("%d: %s", expectError.GetErrorId(), expectError.ShortError())
			Expect(buildStatus.Message).To(ContainSubstring(errorMessage))
		})

		It("should fail to submit PR if build pipeline annotation has non existing pipeline", func() {
			annotationValue := fmt.Sprintf("{\"name\":\"%s\",\"bundle\":\"%s\"}", "wrong-pipeline", "latest")
			annotations := map[string]string{defaultBuildPipelineAnnotation: annotationValue}
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  annotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(resourcePacPrepKey, BuildRequestAnnotationName)

			expectError := boerrors.NewBuildOpError(boerrors.EBuildPipelineInvalid, nil)

			buildStatus := readBuildStatus(getComponent(resourcePacPrepKey))
			Expect(buildStatus).ToNot(BeNil())
			errorMessage := fmt.Sprintf("%d: %s", expectError.GetErrorId(), expectError.ShortError())
			Expect(buildStatus.Message).To(ContainSubstring(errorMessage))
		})

		It("should fail to submit PR if build pipeline annotation is missing bundle and name", func() {
			annotationValue := "{\"some\":\"wrong\"}"
			annotations := map[string]string{defaultBuildPipelineAnnotation: annotationValue}
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  annotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(resourcePacPrepKey, BuildRequestAnnotationName)

			expectError := boerrors.NewBuildOpError(boerrors.EWrongPipelineAnnotation, nil)

			buildStatus := readBuildStatus(getComponent(resourcePacPrepKey))
			Expect(buildStatus).ToNot(BeNil())
			errorMessage := fmt.Sprintf("%d: %s", expectError.GetErrorId(), expectError.ShortError())
			Expect(buildStatus.Message).To(ContainSubstring(errorMessage))
		})

		It("should fail to submit PR if GitHub application is not installed into git repository", func() {
			gpf.CreateGitClient = func(gpf.GitClientConfig) (gp.GitProviderClient, error) {
				return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppNotInstalled,
					fmt.Errorf("GitHub Application is not installed into the repository"))
			}

			EnsurePaCMergeRequestFunc = func(string, *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("Should not invoke merge request creation if GitHub application is not installed into the repository")
				return "url", nil
			}
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			expectError := boerrors.NewBuildOpError(boerrors.EGitHubAppNotInstalled, nil)
			expectPacBuildStatus(resourcePacPrepKey, "error", expectError.GetErrorId(), expectError.ShortError(), "")
		})

		It("should fail to submit PR if unknown git provider is used", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "url", nil
			}

			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				gitURL:       "https://my-git-instance.com/devfile-samples/devfile-sample-java-springboot-basic",
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(resourcePacPrepKey, BuildRequestAnnotationName)

			expectError := boerrors.NewBuildOpError(boerrors.EUnknownGitProvider, nil)
			expectPacBuildStatus(resourcePacPrepKey, "error", expectError.GetErrorId(), expectError.ShortError(), "")
		})

		It("should fail to submit PR if PaC secret is invalid", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			deleteSecret(pacSecretKey)

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    "secret private key",
			}
			createSecret(pacSecretKey, pacSecretData)

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			expectError := boerrors.NewBuildOpError(boerrors.EPaCSecretInvalid, nil)
			expectPacBuildStatus(resourcePacPrepKey, "error", expectError.GetErrorId(), expectError.ShortError(), "")
		})

		It("should fail to submit PR if PaC secret is missing", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			deleteSecret(pacSecretKey)
			deleteSecret(namespacePaCSecretKey)

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			expectError := boerrors.NewBuildOpError(boerrors.EPaCSecretNotFound, nil)
			expectPacBuildStatus(resourcePacPrepKey, "error", expectError.GetErrorId(), expectError.ShortError(), "")
		})

		It("should successfully do PaC provision after error (when PaC GitHub Application was not installed)", func() {
			appNotInstalledErr := boerrors.NewBuildOpError(boerrors.EGitHubAppNotInstalled, nil)
			isCreateGithubClientInvoked := false
			gpf.CreateGitClient = func(gitClientConfig gpf.GitClientConfig) (gp.GitProviderClient, error) {
				isCreateGithubClientInvoked = true
				return nil, appNotInstalledErr
			}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			Eventually(func() bool {
				return isCreateGithubClientInvoked
			}, timeout, interval).Should(BeTrue())

			// Ensure no more retries after permanent error
			gpf.CreateGitClient = func(gitClientConfig gpf.GitClientConfig) (gp.GitProviderClient, error) {
				defer GinkgoRecover()
				Fail("Should not retry PaC provision on permanent error")
				return nil, nil
			}
			Consistently(func() bool {
				expectPacBuildStatus(resourcePacPrepKey, "error", appNotInstalledErr.GetErrorId(), appNotInstalledErr.ShortError(), "")
				return true
			}, ensureTimeout, interval).Should(BeTrue())

			// Suppose PaC GH App is installed
			gpf.CreateGitClient = func(gitClientConfig gpf.GitClientConfig) (gp.GitProviderClient, error) {
				return testGitProviderClient, nil
			}
			mergeUrl := "merge-url"
			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				return mergeUrl, nil
			}

			// Retry
			setComponentBuildRequest(resourcePacPrepKey, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(resourcePacPrepKey, BuildRequestAnnotationName)

			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			expectPacBuildStatus(resourcePacPrepKey, "enabled", 0, "", mergeUrl)
		})

		It("should successfully submit PR with PaC definitions using token", func() {
			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourcePacPrepKey.Name))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.BranchName).ToNot(BeEmpty())
				Expect(d.BaseBranchName).ToNot(BeEmpty())
				Expect(d.Title).ToNot(BeEmpty())
				Expect(d.Text).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return "url", nil
			}

			pacSecretData := map[string]string{"password": "ghp_token"}
			createSCMSecret(namespacePaCSecretKey, pacSecretData, corev1.SecretTypeBasicAuth, map[string]string{})

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			waitSecretCreated(namespacePaCSecretKey)
			waitPaCRepositoryCreated(resourcePacPrepKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
		})

		It("should successfully setup PaC but don't create PaC config proposal PR", func() {
			isEnsurePaCMergeRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isEnsurePaCMergeRequestInvoked = true
				defer GinkgoRecover()
				Fail("Should not create onfig proposal PR if requested not to")
				return "", nil
			}
			isSetupPaCWebhookInvoked := false
			SetupPaCWebhookFunc = func(repoUrl, webhookTargetUrl, webhookSecret string) error {
				isSetupPaCWebhookInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourcePacPrepKey.Name))
				Expect(webhookTargetUrl).ToNot(BeEmpty())
				Expect(webhookSecret).ToNot(BeEmpty())
				return nil
			}

			pacSecretData := map[string]string{"password": "ghp_token"}
			createSCMSecret(namespacePaCSecretKey, pacSecretData, corev1.SecretTypeBasicAuth, map[string]string{})

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCNoMrAnnotationValue)

			waitSecretCreated(namespacePaCSecretKey)
			waitPaCRepositoryCreated(resourcePacPrepKey)
			Eventually(func() bool {
				return isSetupPaCWebhookInvoked
			}, timeout, interval).Should(BeTrue())
			Consistently(func() bool {
				return !isEnsurePaCMergeRequestInvoked
			}, ensureTimeout, interval).Should(BeTrue())
		})

		It("should reuse the same webhook secret for multi component repository", func() {
			var webhookSecretStrings []string
			SetupPaCWebhookFunc = func(repoUrl string, webhookUrl string, webhookSecret string) error {
				webhookSecretStrings = append(webhookSecretStrings, webhookSecret)
				return nil
			}

			pacSecretData := map[string]string{"password": "ghp_token"}
			createSCMSecret(namespacePaCSecretKey, pacSecretData, corev1.SecretTypeBasicAuth, map[string]string{})

			component1Key := resourcePacPrepKey
			component2Key := types.NamespacedName{Name: "component2", Namespace: namespace}
			deleteAllPaCRepositories(component1Key.Namespace)

			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: component1Key,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: component2Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       SampleRepoLink + "-" + resourcePacPrepKey.Name,
				gitRevision:  "main",
			}, BuildRequestConfigurePaCAnnotationValue)

			defer deletePaCRepository(component2Key)
			defer deleteComponent(component2Key)

			waitComponentAnnotationGone(component1Key, BuildRequestAnnotationName)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)

			pacRepository := waitPaCRepositoryCreated(component1Key)
			Expect(pacRepository.OwnerReferences).To(HaveLen(2))

			waitSecretCreated(namespacePaCSecretKey)
			waitSecretCreated(webhookSecretKey)

			Expect(len(webhookSecretStrings)).To(BeNumerically(">", 0))
			for _, webhookSecret := range webhookSecretStrings {
				Expect(webhookSecret).To(Equal(webhookSecretStrings[0]))
			}
		})

		It("should use different webhook secrets for different components of the same application", func() {
			var webhookSecretStrings []string
			SetupPaCWebhookFunc = func(repoUrl string, webhookUrl string, webhookSecret string) error {
				webhookSecretStrings = append(webhookSecretStrings, webhookSecret)
				return nil
			}

			pacSecretData := map[string]string{"password": "ghp_token"}
			createSCMSecret(namespacePaCSecretKey, pacSecretData, corev1.SecretTypeBasicAuth, map[string]string{})

			component1Key := resourcePacPrepKey
			component2Key := types.NamespacedName{Name: "component2", Namespace: namespace}

			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: component1Key,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey:   component2Key,
				annotations:    defaultPipelineAnnotations,
				containerImage: "registry.io/username/image2:tag2",
				gitURL:         "https://github.com/devfile-samples/devfile-sample-go-basic",
			}, BuildRequestConfigurePaCAnnotationValue)
			defer deletePaCRepository(component2Key)
			defer deleteComponent(component2Key)

			waitSecretCreated(namespacePaCSecretKey)
			waitSecretCreated(webhookSecretKey)

			waitPaCRepositoryCreated(component1Key)
			waitPaCRepositoryCreated(component2Key)

			waitComponentAnnotationGone(component1Key, BuildRequestAnnotationName)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)

			Expect(len(webhookSecretStrings)).To(Equal(2))
			Expect(webhookSecretStrings[0]).ToNot(Equal(webhookSecretStrings[1]))
		})

		It("should set error in status if invalid build action requested", func() {
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: resourcePacPrepKey,
				annotations:  defaultPipelineAnnotations,
			}, "non-existing-build-request")
			waitComponentAnnotationGone(resourcePacPrepKey, BuildRequestAnnotationName)

			buildStatus := readBuildStatus(getComponent(resourcePacPrepKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.Message).To(ContainSubstring("unexpected build request"))
		})

		It("should do nothing if a gitsource is missing from component", func() {
			EnsurePaCMergeRequestFunc = func(string, *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			component := &appstudiov1alpha1.Component{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:        resourcePacPrepKey.Name,
					Namespace:   namespace,
					Annotations: defaultPipelineAnnotations,
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName:  resourcePacPrepKey.Name,
					Application:    HASAppName,
					ContainerImage: "quay.io/test/image:latest",
				},
			}
			createComponentCustom(component)
			setComponentBuildRequest(resourcePacPrepKey, BuildRequestConfigurePaCAnnotationValue)

			ensureNoPipelineRunsCreated(resourcePacPrepKey)
		})

		It("should set default branch as base branch when Revision is not set", func() {
			const repoDefaultBranch = "default-branch"
			GetDefaultBranchWithChecksFunc = func(repoUrl string) (string, error) {
				return repoDefaultBranch, nil
			}

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Expect(d.BaseBranchName).To(Equal(repoDefaultBranch))
				for _, file := range d.Files {
					var prYaml tektonapi.PipelineRun
					if err := yaml.Unmarshal(file.Content, &prYaml); err != nil {
						return "", err
					}
					targetBranch := prYaml.Annotations[pacCelExpressionAnnotationName]
					Expect(targetBranch).To(ContainSubstring(fmt.Sprintf(`target_branch == "%s"`, repoDefaultBranch)))
				}
				isCreatePaCPullRequestInvoked = true
				return "url", nil
			}

			component := getSampleComponentData(resourcePacPrepKey)
			// Unset Revision so that GetDefaultBranch function is called to use the default branch
			// set in the remote component repository
			component.Spec.Source.GitSource.Revision = ""
			component.Annotations[BuildRequestAnnotationName] = BuildRequestConfigurePaCAnnotationValue
			component.Annotations[defaultBuildPipelineAnnotation] = defaultPipelineAnnotationValue

			createComponentCustom(component)
			waitComponentAnnotationGone(resourcePacPrepKey, BuildRequestAnnotationName)

			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("Test Pipelines as Code build clean up", func() {
		var (
			namespace             = "build-cleanup"
			resourceCleanupKey    = types.NamespacedName{Name: HASCompName + "-cleanup", Namespace: namespace}
			namespacePaCSecretKey = types.NamespacedName{Name: PipelinesAsCodeGitHubAppSecretName, Namespace: namespace}
			webhookSecretKey      = types.NamespacedName{Name: pipelinesAsCodeWebhooksSecretName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			createNamespace(BuildServiceNamespaceName)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteSecret(webhookSecretKey)
			deleteSecret(namespacePaCSecretKey)

			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
			deleteComponent(resourceCleanupKey)
		})

		It("should successfully unconfigure PaC even when it isn't able to get PaC webhook", func() {
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceCleanupKey)

			pacSecretData = map[string]string{}
			deleteSecret(pacSecretKey)
			createSecret(pacSecretKey, pacSecretData)
			deleteRoute(pacRouteKey)

			setComponentBuildRequest(resourceCleanupKey, BuildRequestUnconfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponentGone(resourceCleanupKey)
			waitDoneMessageOnComponent(resourceCleanupKey)
			expectPacBuildStatus(resourceCleanupKey, "disabled", 0, "", UndoPacMergeRequestURL)
		})

		It("should successfully unconfigure PaC, removal MR not needed", func() {
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceCleanupKey)
			UndoPaCMergeRequestFunc = func(repoUrl string, data *gp.MergeRequestData) (webUrl string, err error) {
				return "", nil
			}

			setComponentBuildRequest(resourceCleanupKey, BuildRequestUnconfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponentGone(resourceCleanupKey)
			waitDoneMessageOnComponent(resourceCleanupKey)
			expectPacBuildStatus(resourceCleanupKey, "disabled", 0, "", "")
		})

		It("should successfully submit PR with PaC definitions removal using GitHub application, remove all incomings and incoming secret (only current component is using)", func() {
			mergeUrl := "merge-url"
			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				isRemovePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourceCleanupKey.Name))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.BranchName).ToNot(BeEmpty())
				Expect(d.BaseBranchName).ToNot(BeEmpty())
				Expect(d.Title).ToNot(BeEmpty())
				Expect(d.Text).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return mergeUrl, nil
			}
			DeletePaCWebhookFunc = func(string, string) error {
				defer GinkgoRecover()
				Fail("Should not try to delete webhook if GitHub application is used")
				return nil
			}

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			// provision component
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceCleanupKey)

			repository := waitPaCRepositoryCreated(resourceCleanupKey)
			incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)

			// update repository with multiple incomings
			incoming := []pacv1alpha1.Incoming{{Type: "webhook-url", Secret: pacv1alpha1.Secret{Name: incomingSecretName, Key: pacIncomingSecretKey}, Targets: []string{"main"}}}
			repository.Spec.Incomings = &incoming
			Expect(k8sClient.Update(ctx, repository)).Should(Succeed())

			// create incoming secret
			component := getComponent(resourceCleanupKey)
			incomingSecretData := map[string]string{
				pacIncomingSecretKey: "secret password",
			}
			incomingSecretResourceKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			createSecret(incomingSecretResourceKey, incomingSecretData)
			defer deleteSecret(incomingSecretResourceKey)

			// unprovision
			setComponentBuildRequest(resourceCleanupKey, BuildRequestUnconfigurePaCAnnotationValue)

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			expectPacBuildStatus(resourceCleanupKey, "disabled", 0, "", mergeUrl)
			waitComponentAnnotationGone(resourceCleanupKey, BuildRequestAnnotationName)
			waitSecretGone(incomingSecretResourceKey)

			repository = waitPaCRepositoryCreated(resourceCleanupKey)
			Expect(repository.Spec.Incomings).To(BeNil())
		})

		It("should successfully submit PR with PaC definitions removal, remove 1 incoming and keep secret for another component", func() {
			// tests multiple entries in incomings for components (when incomings are manually updated)
			mergeUrl := "merge-url"
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				return mergeUrl, nil
			}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return mergeUrl, nil
			}

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			// provision 1st component
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceCleanupKey)
			expectPacBuildStatus(resourceCleanupKey, "enabled", 0, "", mergeUrl)

			// provision 2nd component
			component2Key := types.NamespacedName{Name: "component2", Namespace: namespace}
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: component2Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       SampleRepoLink + "-" + resourceCleanupKey.Name,
				gitRevision:  "another",
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(component2Key)
			expectPacBuildStatus(component2Key, "enabled", 0, "", mergeUrl)
			defer deleteComponent(component2Key)

			component := getComponent(resourceCleanupKey)
			repository := waitPaCRepositoryCreated(resourceCleanupKey)
			incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)

			// update repository with multiple incomings
			incoming := []pacv1alpha1.Incoming{
				// 2 components using same repository and different branches
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{Name: incomingSecretName, Key: pacIncomingSecretKey}, Targets: []string{"main"}},
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{Name: incomingSecretName, Key: pacIncomingSecretKey}, Targets: []string{"another"}}}
			repository.Spec.Incomings = &incoming
			Expect(k8sClient.Update(ctx, repository)).Should(Succeed())

			// create incoming secret
			incomingSecretData := map[string]string{
				pacIncomingSecretKey: "secret password",
			}
			incomingSecretResourceKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			createSecret(incomingSecretResourceKey, incomingSecretData)
			defer deleteSecret(incomingSecretResourceKey)

			// unprovision 1st component
			setComponentBuildRequest(resourceCleanupKey, BuildRequestUnconfigurePaCAnnotationValue)
			expectPacBuildStatus(resourceCleanupKey, "disabled", 0, "", mergeUrl)
			waitComponentAnnotationGone(resourceCleanupKey, BuildRequestAnnotationName)

			// secret will still be there for another component
			waitSecretCreated(incomingSecretResourceKey)

			repository = waitPaCRepositoryCreated(resourceCleanupKey)
			Expect(repository.Spec.Incomings).ToNot(BeNil())
			Expect(len(*repository.Spec.Incomings)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(incomingSecretName))
			Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))
			Expect((*repository.Spec.Incomings)[0].Targets).To(Equal([]string{"another"}))
		})

		It("should successfully submit PR with PaC definitions removal on component deletion", func() {
			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				isRemovePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourceCleanupKey.Name))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.BranchName).ToNot(BeEmpty())
				Expect(d.BaseBranchName).ToNot(BeEmpty())
				Expect(d.Title).ToNot(BeEmpty())
				Expect(d.Text).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return "url", nil
			}
			DeletePaCWebhookFunc = func(string, string) error {
				defer GinkgoRecover()
				Fail("Should not try to delete webhook if GitHub application is used")
				return nil
			}

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceCleanupKey)

			deleteComponent(resourceCleanupKey)

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
		})

		It("should successfully submit merge request with PaC definitions removal using token", func() {
			mergeUrl := "merge-url"
			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				isRemovePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-samerepo"))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.BranchName).ToNot(BeEmpty())
				Expect(d.BaseBranchName).ToNot(BeEmpty())
				Expect(d.Title).ToNot(BeEmpty())
				Expect(d.Text).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return mergeUrl, nil
			}
			DeletePaCWebhookFunc = func(repoUrl string, webhookUrl string) error {
				defer GinkgoRecover()
				Fail("Should not remove webhook because another component is using the same repository")
				return nil
			}

			pacSecretData := map[string]string{"password": "ghp_token"}
			createSCMSecret(namespacePaCSecretKey, pacSecretData, corev1.SecretTypeBasicAuth, map[string]string{})

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				annotations:  defaultPipelineAnnotations,
				gitURL:       SampleRepoLink + "-samerepo",
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceCleanupKey)

			// create second component which uses the same url
			secondComponentKey := types.NamespacedName{Name: HASCompName + "-cleanup-second", Namespace: namespace}
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: secondComponentKey,
				annotations:  defaultPipelineAnnotations,
				gitURL:       SampleRepoLink + "-samerepo",
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(secondComponentKey)

			// shouldn't remove webhook because another component exists for the same repo
			setComponentBuildRequest(resourceCleanupKey, BuildRequestUnconfigurePaCAnnotationValue)

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			expectPacBuildStatus(resourceCleanupKey, "disabled", 0, "", mergeUrl)
			// delete component so there will be just 1 component using the repository
			deleteComponent(resourceCleanupKey)

			isRemovePaCPullRequestInvoked = false
			isDeletePaCWebhookInvoked := false
			DeletePaCWebhookFunc = func(repoUrl string, webhookUrl string) error {
				isDeletePaCWebhookInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-samerepo"))
				Expect(webhookUrl).To(Equal(pacWebhookUrl))
				return nil
			}

			// should remove webhook because there isn't another component which uses the same repo
			setComponentBuildRequest(secondComponentKey, BuildRequestUnconfigurePaCAnnotationValue)

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return isDeletePaCWebhookInvoked
			}, timeout, interval).Should(BeTrue())

			expectPacBuildStatus(secondComponentKey, "disabled", 0, "", mergeUrl)
		})

		It("should not block component deletion if PaC definitions removal failed", func() {
			UndoPaCMergeRequestFunc = func(string, *gp.MergeRequestData) (webUrl string, err error) {
				return "", fmt.Errorf("failed to create PR")
			}
			DeletePaCWebhookFunc = func(string, string) error {
				return fmt.Errorf("failed to delete webhook")
			}

			pacSecretData := map[string]string{"password": "ghp_token"}
			createSCMSecret(namespacePaCSecretKey, pacSecretData, corev1.SecretTypeBasicAuth, map[string]string{})

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceCleanupKey)

			// deleteComponent waits until the component is gone
			deleteComponent(resourceCleanupKey)
		})

		var assertCloseUnmergedMergeRequest = func(expectedBaseBranch string, sourceBranchExists bool) {
			UndoPaCMergeRequestFunc = func(string, *gp.MergeRequestData) (webUrl string, err error) {
				defer GinkgoRecover()
				Fail("Should close unmerged PaC configuration merge request instead of deleting .tekton/ directory")
				return "", nil
			}

			component := getSampleComponentData(resourceCleanupKey)
			if expectedBaseBranch == "" {
				expectedBaseBranch = component.Spec.Source.GitSource.Revision
			} else {
				component.Spec.Source.GitSource.Revision = ""
			}
			component.Annotations[BuildRequestAnnotationName] = BuildRequestConfigurePaCAnnotationValue
			component.Annotations[defaultBuildPipelineAnnotation] = defaultPipelineAnnotationValue

			expectedSourceBranch := pacMergeRequestSourceBranchPrefix + component.Name

			isFindOnboardingMergeRequestInvoked := false
			FindUnmergedPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (*gp.MergeRequest, error) {
				isFindOnboardingMergeRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourceCleanupKey.Name))
				Expect(d.BranchName).Should(Equal(expectedSourceBranch))
				Expect(d.BaseBranchName).Should(Equal(expectedBaseBranch))
				return &gp.MergeRequest{
					WebUrl: "url",
				}, nil
			}

			isDeleteBranchInvoked := false
			DeleteBranchFunc = func(repoUrl string, branchName string) (bool, error) {
				isDeleteBranchInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink + "-" + resourceCleanupKey.Name))
				Expect(branchName).Should(Equal(expectedSourceBranch))
				return sourceBranchExists, nil
			}

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			createComponentCustom(component)
			waitComponentAnnotationGone(resourceCleanupKey, BuildRequestAnnotationName)
			waitPaCFinalizerOnComponent(resourceCleanupKey)

			deleteComponent(resourceCleanupKey)

			Eventually(func() bool {
				return isFindOnboardingMergeRequestInvoked
			}, timeout, interval).Should(BeTrue(),
				"FindUnmergedPaCMergeRequest should have been invoked")
			Eventually(func() bool {
				return isDeleteBranchInvoked
			}, timeout, interval).Should(BeTrue(),
				"DeleteBranch should have been invoked")
		}

		It("should close unmerged PaC pull request opened based on branch specified in Revision", func() {
			assertCloseUnmergedMergeRequest("", true)
		})

		It("should not error when attempt of close unmerged PaC pull request by deleting a non-existing source branch", func() {
			assertCloseUnmergedMergeRequest("", false)
		})

		It("should close unmerged PaC pull request opened based on default branch", func() {
			defaultBranch := "devel"
			GetDefaultBranchWithChecksFunc = func(repoUrl string) (string, error) {
				return defaultBranch, nil
			}
			assertCloseUnmergedMergeRequest(defaultBranch, true)
		})

		It("should fail to unconfigure PaC when pac secret is missing", func() {
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceCleanupKey)
			deleteSecret(pacSecretKey)

			setComponentBuildRequest(resourceCleanupKey, BuildRequestUnconfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponentGone(resourceCleanupKey)
			waitDoneMessageOnComponent(resourceCleanupKey)

			expectError := boerrors.NewBuildOpError(boerrors.EPaCSecretNotFound, nil)
			expectPacBuildStatus(resourceCleanupKey, "error", expectError.GetErrorId(), expectError.ShortError(), "")
		})

		It("should fail to unconfigure PaC if it isn't able to detect git provider", func() {
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			component := getComponent(resourceCleanupKey)
			component.Spec.Source.GitSource.URL = "wrong"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())
			waitPaCFinalizerOnComponent(resourceCleanupKey)

			setComponentBuildRequest(resourceCleanupKey, BuildRequestUnconfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponentGone(resourceCleanupKey)
			waitDoneMessageOnComponent(resourceCleanupKey)

			expectError := boerrors.NewBuildOpError(boerrors.EUnknownGitProvider, nil)
			expectPacBuildStatus(resourceCleanupKey, "error", expectError.GetErrorId(), expectError.ShortError(), "")
		})

		It("should not attempt to create service account on component deletion", func() {
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey: resourceCleanupKey,
				finalizers:   []string{PaCProvisionFinalizer}, // simulate PaC provision was done
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					// simulate PaC provision was done, so it won't update component after provision, which would race with removal of SA, and it could be created again
					BuildStatusAnnotationName: buildStatusPaCProvisionedValue,
				},
			})

			componentBuildSAKey := getComponentServiceAccountKey(resourceCleanupKey)
			waitServiceAccount(componentBuildSAKey)
			// waiting deprecated SA for backward compatibility
			waitPipelineServiceAccount(resourceCleanupKey.Namespace)

			// Make sure that proper cleanup was invoked
			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				isRemovePaCPullRequestInvoked = true
				return "merge-url", nil
			}

			deleteServiceAccount(componentBuildSAKey)
			deletePipelineServiceAccount(resourceCleanupKey.Namespace)

			Expect(isRemovePaCPullRequestInvoked).To(BeFalse())
			// Clean up for the component should not recreate pipeline service account
			deleteComponent(resourceCleanupKey)
			Expect(isRemovePaCPullRequestInvoked).To(BeTrue())

			pipelineServiceAccountKey := types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: resourceCleanupKey.Namespace}
			pipelineServiceAccount := &corev1.ServiceAccount{}
			err := k8sClient.Get(ctx, pipelineServiceAccountKey, pipelineServiceAccount)
			Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

			err = k8sClient.Get(ctx, componentBuildSAKey, pipelineServiceAccount)
			Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("Test Pipelines as Code multi component git repository", func() {
		const (
			multiComponentGitRepositoryUrl = "https://github.com/samples/multi-component-repository"
		)
		var (
			namespace           = "multi-component"
			anotherComponentKey = types.NamespacedName{Name: HASCompName + "-multicomp", Namespace: namespace}
		)

		_ = BeforeEach(func() {
			deleteAllPaCRepositories(anotherComponentKey.Namespace)

			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			createNamespace(BuildServiceNamespaceName)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)
			ResetTestGitProviderClient()

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: anotherComponentKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(anotherComponentKey, BuildRequestAnnotationName)
			waitPaCRepositoryCreated(anotherComponentKey)
		})

		_ = AfterEach(func() {
			ResetTestGitProviderClient()
			deleteComponentPipelineRuns(anotherComponentKey)
			deleteComponent(anotherComponentKey)
			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
			deletePaCRepository(anotherComponentKey)
		})

		It("should reuse existing PaC repository for multi component git repository", func() {
			component1Key := types.NamespacedName{Name: "test-multi-component1", Namespace: namespace}
			component2Key := types.NamespacedName{Name: "test-multi-component2", Namespace: namespace}

			pacRepositoriesList := &pacv1alpha1.RepositoryList{}
			pacRepository := &pacv1alpha1.Repository{}
			err := k8sClient.Get(ctx, component1Key, pacRepository)
			Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
			err = k8sClient.Get(ctx, component2Key, pacRepository)
			Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

			component1PaCMergeRequestCreated := false
			component2PaCMergeRequestCreated := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				if strings.Contains(d.Files[0].FullPath, component1Key.Name) {
					component1PaCMergeRequestCreated = true
					return "url1", nil
				} else if strings.Contains(d.Files[0].FullPath, component2Key.Name) {
					component2PaCMergeRequestCreated = true
					return "url2", nil
				} else {
					Fail("Unknown component in EnsurePaCMergeRequest")
				}
				return "", nil
			}

			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: component1Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       multiComponentGitRepositoryUrl,
			}, BuildRequestConfigurePaCAnnotationValue)
			defer deleteComponent(component1Key)
			waitComponentAnnotationGone(component1Key, BuildRequestAnnotationName)
			Eventually(func() bool { return component1PaCMergeRequestCreated }, timeout, interval).Should(BeTrue())
			waitPaCRepositoryCreated(component1Key)
			defer deletePaCRepository(component1Key)
			Expect(k8sClient.Get(ctx, component1Key, pacRepository)).To(Succeed())
			Expect(pacRepository.OwnerReferences).To(HaveLen(1))
			Expect(pacRepository.OwnerReferences[0].Name).To(Equal(component1Key.Name))
			Expect(pacRepository.OwnerReferences[0].Kind).To(Equal("Component"))
			err = k8sClient.Get(ctx, component2Key, pacRepository)
			Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
			Expect(k8sClient.List(ctx, pacRepositoriesList, &client.ListOptions{Namespace: component1Key.Namespace})).To(Succeed())
			Expect(pacRepositoriesList.Items).To(HaveLen(2)) // 2-nd repository for the anotherComponentKey component

			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: component2Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       multiComponentGitRepositoryUrl,
			}, BuildRequestConfigurePaCAnnotationValue)
			defer deleteComponent(component2Key)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)
			Eventually(func() bool { return component2PaCMergeRequestCreated }, timeout, interval).Should(BeTrue())
			Expect(k8sClient.Get(ctx, component1Key, pacRepository)).To(Succeed())
			Expect(pacRepository.OwnerReferences).To(HaveLen(2))
			Expect(pacRepository.OwnerReferences[0].Name).To(Equal(component1Key.Name))
			Expect(pacRepository.OwnerReferences[0].Kind).To(Equal("Component"))
			Expect(pacRepository.OwnerReferences[1].Name).To(Equal(component2Key.Name))
			Expect(pacRepository.OwnerReferences[1].Kind).To(Equal("Component"))
			err = k8sClient.Get(ctx, component2Key, pacRepository)
			Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
			Expect(k8sClient.List(ctx, pacRepositoriesList, &client.ListOptions{Namespace: component1Key.Namespace})).To(Succeed())
			Expect(pacRepositoriesList.Items).To(HaveLen(2)) // 2-nd repository for the anotherComponentKey component
		})

		It("when 2 components are created with the same url, Pac Repository will be reused, when 1st component is removed and created with new URL, new repository is created", func() {
			deleteComponent(anotherComponentKey)
			deletePaCRepository(anotherComponentKey)
			component1Key := types.NamespacedName{Name: "test-multi-component1", Namespace: namespace}
			component2Key := types.NamespacedName{Name: "test-multi-component2", Namespace: namespace}
			pacRepositoriesList := &pacv1alpha1.RepositoryList{}
			pacRepository := &pacv1alpha1.Repository{}

			Expect(k8sClient.List(ctx, pacRepositoriesList, &client.ListOptions{Namespace: component1Key.Namespace})).To(Succeed())
			Expect(pacRepositoriesList.Items).To(HaveLen(0))

			component1PaCMergeRequestCreated := false
			component2PaCMergeRequestCreated := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				if strings.Contains(d.Files[0].FullPath, component1Key.Name) {
					component1PaCMergeRequestCreated = true
					return "url1", nil
				} else if strings.Contains(d.Files[0].FullPath, component2Key.Name) {
					component2PaCMergeRequestCreated = true
					return "url2", nil
				} else {
					Fail("Unknown component in EnsurePaCMergeRequest")
				}
				return "", nil
			}

			// create 1st component with shared URL
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: component1Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       multiComponentGitRepositoryUrl,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(component1Key, BuildRequestAnnotationName)
			Eventually(func() bool { return component1PaCMergeRequestCreated }, timeout, interval).Should(BeTrue())
			waitPaCRepositoryCreated(component1Key)

			Expect(k8sClient.Get(ctx, component1Key, pacRepository)).To(Succeed())
			Expect(pacRepository.OwnerReferences).To(HaveLen(1))
			Expect(pacRepository.Spec.URL).To(Equal(multiComponentGitRepositoryUrl))
			Expect(k8sClient.List(ctx, pacRepositoriesList, &client.ListOptions{Namespace: component1Key.Namespace})).To(Succeed())
			Expect(pacRepositoriesList.Items).To(HaveLen(1))

			// create 2nd component with shared URL
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: component2Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       multiComponentGitRepositoryUrl,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)
			Eventually(func() bool { return component2PaCMergeRequestCreated }, timeout, interval).Should(BeTrue())

			Expect(k8sClient.Get(ctx, component1Key, pacRepository)).To(Succeed())
			Expect(pacRepository.OwnerReferences).To(HaveLen(2))
			Expect(pacRepository.OwnerReferences[0].Name).To(Equal(component1Key.Name))
			Expect(pacRepository.OwnerReferences[0].Kind).To(Equal("Component"))
			Expect(pacRepository.OwnerReferences[1].Name).To(Equal(component2Key.Name))
			Expect(pacRepository.OwnerReferences[1].Kind).To(Equal("Component"))
			Expect(k8sClient.List(ctx, pacRepositoriesList, &client.ListOptions{Namespace: component1Key.Namespace})).To(Succeed())
			Expect(pacRepositoriesList.Items).To(HaveLen(1))

			// remove 1st component
			deleteComponent(component1Key)
			Expect(k8sClient.Get(ctx, component1Key, pacRepository)).To(Succeed())

			// create 1st component again, but with different URL, which will create new Pac repository and not use the one which has same name as component
			differentGitRepositoryUrl := "https://github.com/samples/multi-different-component-repository"
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: component1Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       differentGitRepositoryUrl,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(component1Key, BuildRequestAnnotationName)
			Eventually(func() bool { return component1PaCMergeRequestCreated }, timeout, interval).Should(BeTrue())

			Expect(k8sClient.List(ctx, pacRepositoriesList, &client.ListOptions{Namespace: component1Key.Namespace})).To(Succeed())
			Expect(pacRepositoriesList.Items).To(HaveLen(2)) // new repository will be created with added randomStr suffix
			originalRepositoryFound := false
			newRepositoryFound := false
			for _, repo := range pacRepositoriesList.Items {
				// original repository will be still named after component1 even though component1 was removed, because component2 was using it
				if repo.Name == component1Key.Name {
					Expect(repo.Spec.URL).To(Equal(multiComponentGitRepositoryUrl))
					// not testing ownerReferences because it will still have 2 references even after component1 removal, because
					// in tests we aren't running controller which removes references from Repository upon component removal
					originalRepositoryFound = true
					// new repository for component1 will be named with prefix of component1 and random string, because component1 name repository has different url
				} else if strings.HasPrefix(repo.Name, fmt.Sprintf("%s-", component1Key.Name)) {
					Expect(repo.Spec.URL).To(Equal(differentGitRepositoryUrl))
					Expect(repo.OwnerReferences).To(HaveLen(1))
					Expect(repo.OwnerReferences[0].Name).To(Equal(component1Key.Name))
					Expect(repo.OwnerReferences[0].Kind).To(Equal("Component"))
					newRepositoryFound = true
				}
			}
			Expect(originalRepositoryFound).To(BeTrue())
			Expect(newRepositoryFound).To(BeTrue())
		})

	})

	Context("Test Pipelines as Code trigger build", func() {
		var (
			namespace             = "trigger-build"
			resourcePacTriggerKey = types.NamespacedName{Name: HASCompName + "-pactrigger", Namespace: namespace}
			namespacePaCSecretKey = types.NamespacedName{Name: PipelinesAsCodeGitHubAppSecretName, Namespace: namespace}
			webhookSecretKey      = types.NamespacedName{Name: pipelinesAsCodeWebhooksSecretName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			createNamespace(BuildServiceNamespaceName)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponent(resourcePacTriggerKey)
			deletePaCRepository(resourcePacTriggerKey)

			deleteSecret(webhookSecretKey)
			deleteSecret(namespacePaCSecretKey)

			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
		})

		It("should successfully trigger PaC build", func() {
			mergeUrl := "merge-url"

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return mergeUrl, nil
			}

			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacTriggerKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourcePacTriggerKey)
			expectPacBuildStatus(resourcePacTriggerKey, "enabled", 0, "", mergeUrl)

			repository := waitPaCRepositoryCreated(resourcePacTriggerKey)
			component := getComponent(resourcePacTriggerKey)

			pacWebhookRoute := &routev1.Route{}
			Expect(k8sClient.Get(ctx, pacRouteKey, pacWebhookRoute)).To(Succeed())
			webhookTargetUrl := "https://" + pacWebhookRoute.Spec.Host
			pipelineRunName := component.Name + pipelineRunOnPushSuffix

			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			gock.InterceptClient(client)

			getHttpClientMocked := func() *http.Client {
				return client
			}
			GetHttpClientFunction = getHttpClientMocked

			defer gock.Off()

			setComponentBuildRequest(resourcePacTriggerKey, BuildRequestTriggerPaCBuildAnnotationValue)

			incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)
			incomingSecretResourceKey := types.NamespacedName{Namespace: component.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretResourceKey)
			defer deleteSecret(incomingSecretResourceKey)

			incomingSecret := corev1.Secret{}
			Expect(k8sClient.Get(ctx, incomingSecretResourceKey, &incomingSecret)).To(Succeed())
			secretValue := string(incomingSecret.Data[pacIncomingSecretKey][:])

			req := gock.New(webhookTargetUrl).
				BodyString("").
				Post("/incoming").
				MatchParam("secret", secretValue).
				MatchParam("repository", component.Name).
				MatchParam("branch", "main").
				MatchParam("pipelinerun", pipelineRunName)
			req.Reply(202).JSON(map[string]string{})

			waitComponentAnnotationGone(resourcePacTriggerKey, BuildRequestAnnotationName)

			repository = waitPaCRepositoryCreated(resourcePacTriggerKey)

			Expect(repository.Spec.Incomings).ToNot(BeNil())
			Expect(len(*repository.Spec.Incomings)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(incomingSecretName))
			Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))
			Expect((*repository.Spec.Incomings)[0].Targets).To(Equal([]string{"main"}))
			Expect(len((*repository.Spec.Incomings)[0].Params)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Params).To(Equal([]string{"source_url"}))
		})

		It("should successfully trigger builds for 2 components with different branches in the same repo", func() {
			mergeUrl := "merge-url"

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return mergeUrl, nil
			}

			// provision 1st component
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacTriggerKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourcePacTriggerKey)
			expectPacBuildStatus(resourcePacTriggerKey, "enabled", 0, "", mergeUrl)
			repository := waitPaCRepositoryCreated(resourcePacTriggerKey)

			// provision 2nd component
			component2Key := types.NamespacedName{Name: "component2", Namespace: namespace}
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: component2Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       SampleRepoLink + "-" + resourcePacTriggerKey.Name,
				gitRevision:  "another",
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(component2Key)
			expectPacBuildStatus(component2Key, "enabled", 0, "", mergeUrl)
			defer deleteComponent(component2Key)

			pacWebhookRoute := &routev1.Route{}
			Expect(k8sClient.Get(ctx, pacRouteKey, pacWebhookRoute)).To(Succeed())
			webhookTargetUrl := "https://" + pacWebhookRoute.Spec.Host
			incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)

			component1 := getComponent(resourcePacTriggerKey)
			pipelineRunName1 := component1.Name + pipelineRunOnPushSuffix

			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			gock.InterceptClient(client)

			getHttpClientMocked := func() *http.Client {
				return client
			}
			GetHttpClientFunction = getHttpClientMocked

			defer gock.Off()

			req := gock.New(webhookTargetUrl).
				BodyString("").
				Post("/incoming").
				MatchParam("repository", component1.Name).
				MatchParam("branch", "main").
				MatchParam("pipelinerun", pipelineRunName1)
			req.Reply(202).JSON(map[string]string{})

			// trigger push pipeline for 1st component
			setComponentBuildRequest(resourcePacTriggerKey, BuildRequestTriggerPaCBuildAnnotationValue)

			incomingSecretResourceKey := types.NamespacedName{Namespace: component1.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretResourceKey)
			defer deleteSecret(incomingSecretResourceKey)
			waitComponentAnnotationGone(resourcePacTriggerKey, BuildRequestAnnotationName)

			repository = waitPaCRepositoryCreated(resourcePacTriggerKey)
			Expect(repository.Spec.Incomings).ToNot(BeNil())
			Expect(len(*repository.Spec.Incomings)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(incomingSecretName))
			Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))
			Expect((*repository.Spec.Incomings)[0].Targets).To(Equal([]string{"main"}))

			component2 := getComponent(component2Key)
			pipelineRunName2 := component2.Name + pipelineRunOnPushSuffix

			req = gock.New(webhookTargetUrl).
				BodyString("").
				Post("/incoming").
				MatchParam("repository", component1.Name).
				MatchParam("branch", "another").
				MatchParam("pipelinerun", pipelineRunName2)
			req.Reply(202).JSON(map[string]string{})

			// trigger push pipeline for 2nd component
			setComponentBuildRequest(component2Key, BuildRequestTriggerPaCBuildAnnotationValue)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)

			repository = waitPaCRepositoryCreated(resourcePacTriggerKey)
			Expect(repository.Spec.Incomings).ToNot(BeNil())
			Expect(len(*repository.Spec.Incomings)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(incomingSecretName))
			Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))
			Expect((*repository.Spec.Incomings)[0].Targets).To(Equal([]string{"main", "another"}))
			Expect(len((*repository.Spec.Incomings)[0].Params)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Params).To(Equal([]string{"source_url"}))
		})

		It("should successfully trigger builds for 2 components with the same branches in the same repo", func() {
			mergeUrl := "merge-url"

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return mergeUrl, nil
			}

			// provision 1st component
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacTriggerKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourcePacTriggerKey)
			expectPacBuildStatus(resourcePacTriggerKey, "enabled", 0, "", mergeUrl)
			repository := waitPaCRepositoryCreated(resourcePacTriggerKey)

			// provision 2nd component
			component2Key := types.NamespacedName{Name: "component2", Namespace: namespace}
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: component2Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       SampleRepoLink + "-" + resourcePacTriggerKey.Name,
				gitRevision:  "main",
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(component2Key)
			expectPacBuildStatus(component2Key, "enabled", 0, "", mergeUrl)
			defer deleteComponent(component2Key)

			pacWebhookRoute := &routev1.Route{}
			Expect(k8sClient.Get(ctx, pacRouteKey, pacWebhookRoute)).To(Succeed())
			webhookTargetUrl := "https://" + pacWebhookRoute.Spec.Host
			incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)

			component1 := getComponent(resourcePacTriggerKey)
			pipelineRunName1 := component1.Name + pipelineRunOnPushSuffix

			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			gock.InterceptClient(client)

			getHttpClientMocked := func() *http.Client {
				return client
			}
			GetHttpClientFunction = getHttpClientMocked

			defer gock.Off()

			req := gock.New(webhookTargetUrl).
				BodyString("").
				Post("/incoming").
				MatchParam("repository", component1.Name).
				MatchParam("branch", "main").
				MatchParam("pipelinerun", pipelineRunName1)
			req.Reply(202).JSON(map[string]string{})

			// trigger push pipeline for 1st component
			setComponentBuildRequest(resourcePacTriggerKey, BuildRequestTriggerPaCBuildAnnotationValue)

			incomingSecretResourceKey := types.NamespacedName{Namespace: component1.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretResourceKey)
			defer deleteSecret(incomingSecretResourceKey)
			waitComponentAnnotationGone(resourcePacTriggerKey, BuildRequestAnnotationName)

			repository = waitPaCRepositoryCreated(resourcePacTriggerKey)
			Expect(repository.Spec.Incomings).ToNot(BeNil())
			Expect(len(*repository.Spec.Incomings)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(incomingSecretName))
			Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))
			Expect((*repository.Spec.Incomings)[0].Targets).To(Equal([]string{"main"}))

			component2 := getComponent(component2Key)
			pipelineRunName2 := component2.Name + pipelineRunOnPushSuffix

			req = gock.New(webhookTargetUrl).
				BodyString("").
				Post("/incoming").
				MatchParam("repository", component1.Name).
				MatchParam("branch", "main").
				MatchParam("pipelinerun", pipelineRunName2)
			req.Reply(202).JSON(map[string]string{})

			// trigger push pipeline for 2nd component
			setComponentBuildRequest(component2Key, BuildRequestTriggerPaCBuildAnnotationValue)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)

			repository = waitPaCRepositoryCreated(resourcePacTriggerKey)
			Expect(repository.Spec.Incomings).ToNot(BeNil())
			Expect(len(*repository.Spec.Incomings)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(incomingSecretName))
			Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))
			Expect((*repository.Spec.Incomings)[0].Targets).To(Equal([]string{"main"}))
			Expect(len((*repository.Spec.Incomings)[0].Params)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Params).To(Equal([]string{"source_url"}))
		})

		It("should successfully trigger PaC build, multiple incomings exist, even with wrong secret", func() {
			mergeUrl := "merge-url"

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return mergeUrl, nil
			}

			// provision 1st component
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacTriggerKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourcePacTriggerKey)
			expectPacBuildStatus(resourcePacTriggerKey, "enabled", 0, "", mergeUrl)

			repository := waitPaCRepositoryCreated(resourcePacTriggerKey)
			incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)

			// update repository with multiple incomings, even with wrong secret
			incoming := []pacv1alpha1.Incoming{
				// correct secret name for main branch
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{Name: incomingSecretName, Key: pacIncomingSecretKey}, Targets: []string{"main"}},
				// missing secret for first branch
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{}, Targets: []string{"first"}},
				// wrong secret for second branch
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{Name: "wrong_name", Key: pacIncomingSecretKey}, Targets: []string{"second"}}}
			repository.Spec.Incomings = &incoming
			Expect(k8sClient.Update(ctx, repository)).Should(Succeed())

			// provision 2nd component
			component2Key := types.NamespacedName{Name: "component2", Namespace: namespace}
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: component2Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       SampleRepoLink + "-" + resourcePacTriggerKey.Name,
				gitRevision:  "another",
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(component2Key)
			expectPacBuildStatus(component2Key, "enabled", 0, "", mergeUrl)
			defer deleteComponent(component2Key)

			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			gock.InterceptClient(client)

			getHttpClientMocked := func() *http.Client {
				return client
			}
			GetHttpClientFunction = getHttpClientMocked

			defer gock.Off()

			pacWebhookRoute := &routev1.Route{}
			Expect(k8sClient.Get(ctx, pacRouteKey, pacWebhookRoute)).To(Succeed())
			webhookTargetUrl := "https://" + pacWebhookRoute.Spec.Host

			component2 := getComponent(component2Key)
			pipelineRunName2 := component2.Name + pipelineRunOnPushSuffix

			req := gock.New(webhookTargetUrl).
				BodyString("").
				Post("/incoming").
				MatchParam("repository", repository.Name).
				MatchParam("branch", "another").
				MatchParam("pipelinerun", pipelineRunName2)
			req.Reply(202).JSON(map[string]string{})

			// trigger push pipeline for 2nd component
			setComponentBuildRequest(component2Key, BuildRequestTriggerPaCBuildAnnotationValue)
			incomingSecretResourceKey := types.NamespacedName{Namespace: component2.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretResourceKey)
			defer deleteSecret(incomingSecretResourceKey)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)

			repository = waitPaCRepositoryCreated(resourcePacTriggerKey)
			Expect(repository.Spec.Incomings).ToNot(BeNil())
			Expect(len(*repository.Spec.Incomings)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(incomingSecretName))
			Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))
			Expect((*repository.Spec.Incomings)[0].Targets).To(Equal([]string{"main", "first", "second", "another"}))
			Expect(len((*repository.Spec.Incomings)[0].Params)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Params).To(Equal([]string{"source_url"}))
		})

		It("should successfully trigger PaC build, multiple incomings exist, even with wrong secret, branch found but without secret", func() {
			mergeUrl := "merge-url"

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return mergeUrl, nil
			}

			// provision 1st component
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacTriggerKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourcePacTriggerKey)
			expectPacBuildStatus(resourcePacTriggerKey, "enabled", 0, "", mergeUrl)

			repository := waitPaCRepositoryCreated(resourcePacTriggerKey)
			incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)

			// update repository with multiple incomings, even with wrong secret
			incoming := []pacv1alpha1.Incoming{
				// missing secret for main branch
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{}, Targets: []string{"main"}},
				// missing secret for another branch (used for 2nd component)
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{}, Targets: []string{"another"}},
				// missing secret for first branch
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{}, Targets: []string{"first"}},
				// wrong secret for second branch
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{Name: "wrong_name", Key: pacIncomingSecretKey}, Targets: []string{"second"}}}
			repository.Spec.Incomings = &incoming
			Expect(k8sClient.Update(ctx, repository)).Should(Succeed())

			// provision 2nd component
			component2Key := types.NamespacedName{Name: "component2", Namespace: namespace}
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: component2Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       SampleRepoLink + "-" + resourcePacTriggerKey.Name,
				gitRevision:  "another",
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(component2Key)
			expectPacBuildStatus(component2Key, "enabled", 0, "", mergeUrl)
			defer deleteComponent(component2Key)

			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			gock.InterceptClient(client)

			getHttpClientMocked := func() *http.Client {
				return client
			}
			GetHttpClientFunction = getHttpClientMocked

			defer gock.Off()

			pacWebhookRoute := &routev1.Route{}
			Expect(k8sClient.Get(ctx, pacRouteKey, pacWebhookRoute)).To(Succeed())
			webhookTargetUrl := "https://" + pacWebhookRoute.Spec.Host

			component2 := getComponent(component2Key)
			pipelineRunName2 := component2.Name + pipelineRunOnPushSuffix

			req := gock.New(webhookTargetUrl).
				BodyString("").
				Post("/incoming").
				MatchParam("repository", repository.Name).
				MatchParam("branch", "another").
				MatchParam("pipelinerun", pipelineRunName2)
			req.Reply(202).JSON(map[string]string{})

			// trigger push pipeline for 2nd component
			setComponentBuildRequest(component2Key, BuildRequestTriggerPaCBuildAnnotationValue)
			incomingSecretResourceKey := types.NamespacedName{Namespace: component2.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretResourceKey)
			defer deleteSecret(incomingSecretResourceKey)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)

			repository = waitPaCRepositoryCreated(resourcePacTriggerKey)
			Expect(repository.Spec.Incomings).ToNot(BeNil())
			Expect(len(*repository.Spec.Incomings)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(incomingSecretName))
			Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))
			Expect((*repository.Spec.Incomings)[0].Targets).To(Equal([]string{"main", "another", "first", "second"}))
			Expect(len((*repository.Spec.Incomings)[0].Params)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Params).To(Equal([]string{"source_url"}))
		})

		It("should successfully trigger PaC build, one incomings exist, but without secret", func() {
			mergeUrl := "merge-url"

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return mergeUrl, nil
			}

			// provision 1st component
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: resourcePacTriggerKey,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourcePacTriggerKey)
			expectPacBuildStatus(resourcePacTriggerKey, "enabled", 0, "", mergeUrl)

			repository := waitPaCRepositoryCreated(resourcePacTriggerKey)
			incomingSecretName := fmt.Sprintf("%s%s", repository.Name, pacIncomingSecretNameSuffix)

			// update repository with multiple incomings, even with wrong secret
			incoming := []pacv1alpha1.Incoming{
				// missing secret for main branch
				{Type: "webhook-url", Secret: pacv1alpha1.Secret{}, Targets: []string{"main"}}}
			repository.Spec.Incomings = &incoming
			Expect(k8sClient.Update(ctx, repository)).Should(Succeed())

			// provision 2nd component
			component2Key := types.NamespacedName{Name: "component2", Namespace: namespace}
			createComponentAndProcessBuildRequest(componentConfig{
				componentKey: component2Key,
				annotations:  defaultPipelineAnnotations,
				gitURL:       SampleRepoLink + "-" + resourcePacTriggerKey.Name,
				gitRevision:  "another",
			}, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(component2Key)
			expectPacBuildStatus(component2Key, "enabled", 0, "", mergeUrl)
			defer deleteComponent(component2Key)

			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			gock.InterceptClient(client)

			getHttpClientMocked := func() *http.Client {
				return client
			}
			GetHttpClientFunction = getHttpClientMocked

			defer gock.Off()

			pacWebhookRoute := &routev1.Route{}
			Expect(k8sClient.Get(ctx, pacRouteKey, pacWebhookRoute)).To(Succeed())
			webhookTargetUrl := "https://" + pacWebhookRoute.Spec.Host

			component2 := getComponent(component2Key)
			pipelineRunName2 := component2.Name + pipelineRunOnPushSuffix

			req := gock.New(webhookTargetUrl).
				BodyString("").
				Post("/incoming").
				MatchParam("repository", repository.Name).
				MatchParam("branch", "another").
				MatchParam("pipelinerun", pipelineRunName2)
			req.Reply(202).JSON(map[string]string{})

			// trigger push pipeline for 2nd component
			setComponentBuildRequest(component2Key, BuildRequestTriggerPaCBuildAnnotationValue)
			incomingSecretResourceKey := types.NamespacedName{Namespace: component2.Namespace, Name: incomingSecretName}
			waitSecretCreated(incomingSecretResourceKey)
			defer deleteSecret(incomingSecretResourceKey)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)

			repository = waitPaCRepositoryCreated(resourcePacTriggerKey)
			Expect(repository.Spec.Incomings).ToNot(BeNil())
			Expect(len(*repository.Spec.Incomings)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(incomingSecretName))
			Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))
			Expect((*repository.Spec.Incomings)[0].Targets).To(Equal([]string{"main", "another"}))
			Expect(len((*repository.Spec.Incomings)[0].Params)).To(Equal(1))
			Expect((*repository.Spec.Incomings)[0].Params).To(Equal([]string{"source_url"}))
		})
	})

	// TODO delete when migration is done
	Context("Test Pipelines as Code Service Account migration PR", func() {
		const (
			mergeUrl                       = "merge-url"
			buildStatusPaCProvisionedValue = `{"pac":{"state":"enabled","merge-url":"https://githost.com/org/repo/pull/123","configuration-time":"Wed, 06 Nov 2024 08:41:44 UTC"},"message":"done"}`
		)
		var (
			namespace             = "after-migration"
			resourceMigrationKey  = types.NamespacedName{Name: HASCompName + "-sa-migration", Namespace: namespace}
			namespacePaCSecretKey = types.NamespacedName{Name: PipelinesAsCodeGitHubAppSecretName, Namespace: namespace}
			webhookSecretKey      = types.NamespacedName{Name: pipelinesAsCodeWebhooksSecretName, Namespace: namespace}
		)

		_ = BeforeEach(func() {
			createNamespace(namespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			createNamespace(BuildServiceNamespaceName)
			createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			ResetTestGitProviderClient()
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should be checked")
				return "", nil
			}

			createCustomComponentWithoutBuildRequest(componentConfig{
				componentKey: resourceMigrationKey,
				annotations: map[string]string{
					defaultBuildPipelineAnnotation: defaultPipelineAnnotationValue,
					BuildStatusAnnotationName:      buildStatusPaCProvisionedValue, // simulate PaC provision was done
				},
				finalizers: []string{PaCProvisionFinalizer}, // simulate PaC provision was done
			})
		})

		_ = AfterEach(func() {
			deleteComponent(resourceMigrationKey)
			deletePaCRepository(resourceMigrationKey)

			deleteSecret(webhookSecretKey)
			deleteSecret(namespacePaCSecretKey)

			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
		})

		It("should create migration PR", func() {
			isCreateSAMigrationPRInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					buildPipelineData := &tektonapi.PipelineRun{}
					Expect(yaml.Unmarshal(file.Content, buildPipelineData)).To(Succeed())
					Expect(buildPipelineData.Spec.TaskRunTemplate.ServiceAccountName).To(Equal(getComponentServiceAccountKey(resourceMigrationKey).Name))
				}

				isCreateSAMigrationPRInvoked = true
				return mergeUrl + "-migration", nil
			}
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				return yaml.Marshal(tektonapi.PipelineRun{})
			}

			component := getComponent(resourceMigrationKey)
			component.Annotations[serviceAccountMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				return isCreateSAMigrationPRInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationGone(resourceMigrationKey, serviceAccountMigrationAnnotationName)
		})

		It("should create migration PR if only push pipeline present", func() {
			isCreateSAMigrationPRInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				Expect(len(d.Files)).To(Equal(1))
				buildPipelineData := &tektonapi.PipelineRun{}
				Expect(yaml.Unmarshal(d.Files[0].Content, buildPipelineData)).To(Succeed())
				Expect(buildPipelineData.Name).To(HaveSuffix(pipelineRunOnPushSuffix))
				Expect(buildPipelineData.Spec.TaskRunTemplate.ServiceAccountName).To(Equal(getComponentServiceAccountKey(resourceMigrationKey).Name))

				isCreateSAMigrationPRInvoked = true
				return mergeUrl + "-migration", nil
			}
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				if strings.HasSuffix(filePath, pipelineRunOnPushFilename) {
					return yaml.Marshal(
						tektonapi.PipelineRun{
							ObjectMeta: metav1.ObjectMeta{
								Name: resourceMigrationKey.Name + pipelineRunOnPushSuffix,
							},
						})
				}
				return nil, k8sErrors.NewNotFound(schema.GroupResource{}, "pull pipeline")
			}

			component := getComponent(resourceMigrationKey)
			component.Annotations[serviceAccountMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				return isCreateSAMigrationPRInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationGone(resourceMigrationKey, serviceAccountMigrationAnnotationName)
		})

		It("should create migration PR if only pull pipeline present", func() {
			isCreateSAMigrationPRInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()

				Expect(len(d.Files)).To(Equal(1))
				buildPipelineData := &tektonapi.PipelineRun{}
				Expect(yaml.Unmarshal(d.Files[0].Content, buildPipelineData)).To(Succeed())
				Expect(buildPipelineData.Name).To(HaveSuffix(pipelineRunOnPRSuffix))
				Expect(buildPipelineData.Spec.TaskRunTemplate.ServiceAccountName).To(Equal(getComponentServiceAccountKey(resourceMigrationKey).Name))

				isCreateSAMigrationPRInvoked = true
				return mergeUrl + "-migration", nil
			}
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				if strings.HasSuffix(filePath, pipelineRunOnPRFilename) {
					return yaml.Marshal(
						tektonapi.PipelineRun{
							ObjectMeta: metav1.ObjectMeta{
								Name: resourceMigrationKey.Name + pipelineRunOnPRSuffix,
							},
						})
				}
				return nil, k8sErrors.NewNotFound(schema.GroupResource{}, "push pipeline")
			}

			component := getComponent(resourceMigrationKey)
			component.Annotations[serviceAccountMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				return isCreateSAMigrationPRInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationGone(resourceMigrationKey, serviceAccountMigrationAnnotationName)
		})

		It("should recover after transient errors and create migration PR", func() {
			isCreateSAMigrationPRInvoked := false
			errorCounter := 0
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				if errorCounter < 5 {
					errorCounter++
					return "", fmt.Errorf("faile to do something")
				}

				defer GinkgoRecover()

				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					buildPipelineData := &tektonapi.PipelineRun{}
					Expect(yaml.Unmarshal(file.Content, buildPipelineData)).To(Succeed())
					Expect(buildPipelineData.Spec.TaskRunTemplate.ServiceAccountName).To(Equal(getComponentServiceAccountKey(resourceMigrationKey).Name))
				}

				isCreateSAMigrationPRInvoked = true
				return mergeUrl + "-migration", nil
			}
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				return yaml.Marshal(tektonapi.PipelineRun{})
			}

			component := getComponent(resourceMigrationKey)
			component.Annotations[serviceAccountMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				return isCreateSAMigrationPRInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationGone(resourceMigrationKey, serviceAccountMigrationAnnotationName)
		})

		It("should not create migration PR and stop on persistent error", func() {
			isCreateSAMigrationPRInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				isCreateSAMigrationPRInvoked = true
				Fail("should not invoke migration PR creation")
				return "", nil
			}
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				return nil, boerrors.NewBuildOpError(boerrors.EGitHubAppNotInstalled, fmt.Errorf("error"))
			}

			component := getComponent(resourceMigrationKey)
			component.Annotations[serviceAccountMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Consistently(func() bool {
				return !isCreateSAMigrationPRInvoked
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			waitComponentAnnotationGone(resourceMigrationKey, serviceAccountMigrationAnnotationName)
		})

		It("should stop if persistent error occurs on migration PR creation", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return "", boerrors.NewBuildOpError(boerrors.EGitHubAppNotInstalled, fmt.Errorf("error"))
			}
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				return yaml.Marshal(tektonapi.PipelineRun{})
			}

			component := getComponent(resourceMigrationKey)
			component.Annotations[serviceAccountMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			waitComponentAnnotationGone(resourceMigrationKey, serviceAccountMigrationAnnotationName)
		})

		It("should not create migration PR if pipeline definitions not found", func() {
			isCreateSAMigrationPRInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				isCreateSAMigrationPRInvoked = true
				Fail("should not invoke migration PR creation")
				return "", nil
			}
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				return nil, k8sErrors.NewNotFound(schema.GroupResource{}, "pipeline")
			}

			component := getComponent(resourceMigrationKey)
			component.Annotations[serviceAccountMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Consistently(func() bool {
				return !isCreateSAMigrationPRInvoked
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			waitComponentAnnotationGone(resourceMigrationKey, serviceAccountMigrationAnnotationName)
		})

		It("should not create migration PR if custom Service Account is used", func() {
			isCreateSAMigrationPRInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				isCreateSAMigrationPRInvoked = true
				Fail("should not invoke migration PR creation")
				return "", nil
			}
			DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
				return yaml.Marshal(
					tektonapi.PipelineRun{
						Spec: tektonapi.PipelineRunSpec{
							TaskRunTemplate: tektonapi.PipelineTaskRunTemplate{
								ServiceAccountName: "my-sa",
							},
						},
					})
			}

			component := getComponent(resourceMigrationKey)
			component.Annotations[serviceAccountMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Consistently(func() bool {
				return !isCreateSAMigrationPRInvoked
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
			waitComponentAnnotationGone(resourceMigrationKey, serviceAccountMigrationAnnotationName)
		})
	})
})
