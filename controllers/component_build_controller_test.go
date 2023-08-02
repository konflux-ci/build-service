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
	"fmt"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	buildappstudiov1alpha1 "github.com/redhat-appstudio/build-service/api/v1alpha1"
	"github.com/redhat-appstudio/build-service/pkg/boerrors"
	gp "github.com/redhat-appstudio/build-service/pkg/git/gitprovider"
	gpf "github.com/redhat-appstudio/build-service/pkg/git/gitproviderfactory"
	appstudiospiapiv1beta1 "github.com/redhat-appstudio/service-provider-integration-operator/api/v1beta1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	//+kubebuilder:scaffold:imports
)

const (
	githubAppPrivateKey = `-----BEGIN RSA PRIVATE KEY-----
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

var _ = Describe("Component initial build controller", func() {

	const (
		pacHost       = "pac-host"
		pacWebhookUrl = "https://" + pacHost
	)

	var (
		// All related to the component resources have the same key (but different type)
		resourceKey           = types.NamespacedName{Name: HASCompName, Namespace: HASAppNamespace}
		pacRouteKey           = types.NamespacedName{Name: pipelinesAsCodeRouteName, Namespace: pipelinesAsCodeNamespace}
		pacSecretKey          = types.NamespacedName{Name: gitopsprepare.PipelinesAsCodeSecretName, Namespace: buildServiceNamespaceName}
		namespacePaCSecretKey = types.NamespacedName{Name: gitopsprepare.PipelinesAsCodeSecretName, Namespace: HASAppNamespace}
		webhookSecretKey      = types.NamespacedName{Name: gitops.PipelinesAsCodeWebhooksSecretName, Namespace: HASAppNamespace}
	)

	BeforeEach(func() {
		createNamespace(buildServiceNamespaceName)
		createBuildPipelineRunSelector(defaultSelectorKey)
	})

	AfterEach(func() {
		deleteBuildPipelineRunSelector(defaultSelectorKey)
	})

	Context("Test Pipelines as Code build preparation [deprecated annotations]", func() {

		_ = BeforeEach(func() {
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			createNamespace(buildServiceNamespaceName)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			ResetTestGitProviderClient()

			createComponentForPaCBuild(getSampleComponentData(resourceKey))
		})

		_ = AfterEach(func() {
			deleteComponent(resourceKey)

			deleteSecret(webhookSecretKey)
			deleteSecret(namespacePaCSecretKey)

			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
		})

		It("should successfully submit PR with PaC definitions using GitHub application and set PaC annotation", func() {
			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
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
				return "merge-url", nil
			}
			SetupPaCWebhookFunc = func(string, string, string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			setComponentDevfileModel(resourceKey)

			waitPaCRepositoryCreated(resourceKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationValue(resourceKey, PaCProvisionAnnotationName, PaCProvisionDoneAnnotationValue)
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

			setComponentDevfileModel(resourceKey)

			waitComponentAnnotationValue(resourceKey,
				PaCProvisionAnnotationName, PaCProvisionErrorAnnotationValue)
			expectedErr := boerrors.NewBuildOpError(
				boerrors.EGitHubAppNotInstalled, fmt.Errorf("something is wrong"))
			waitComponentAnnotationValue(resourceKey,
				PaCProvisionErrorDetailsAnnotationName, expectedErr.ShortError())
		})

		It("should fail to submit PR if unknown git provider is used", func() {
			component := getComponent(resourceKey)
			invalidGitUrl := strings.Replace(SampleRepoLink, "github.com", "outer-space.git", 1)
			component.Spec.Source.GitSource.URL = invalidGitUrl
			Expect(k8sClient.Update(ctx, component)).To(Succeed())
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, resourceKey, component)).To(Succeed())
				return component.Spec.Source.GitSource.URL == invalidGitUrl
			}, timeout, interval).Should(BeTrue())

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				return "url", nil
			}

			setComponentDevfileModel(resourceKey)

			waitComponentAnnotationValue(resourceKey,
				PaCProvisionAnnotationName, PaCProvisionErrorAnnotationValue)
			expectedErr := boerrors.NewBuildOpError(
				boerrors.EUnknownGitProvider, fmt.Errorf("something is wrong"))
			waitComponentAnnotationValue(resourceKey,
				PaCProvisionErrorDetailsAnnotationName, expectedErr.ShortError())

			Expect(isCreatePaCPullRequestInvoked).Should(BeFalse())
		})

		It("should fail to submit PR if PaC secret is invalid", func() {
			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				return "url", nil
			}

			deleteSecret(pacSecretKey)

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    "secret private key",
			}
			createSecret(pacSecretKey, pacSecretData)
			var pacSecret corev1.Secret
			Eventually(func() error {
				return k8sClient.Get(ctx, pacSecretKey, &pacSecret)
			}, timeout, interval).Should(Succeed())

			setComponentDevfileModel(resourceKey)

			waitComponentAnnotationValue(resourceKey,
				PaCProvisionAnnotationName, PaCProvisionErrorAnnotationValue)
			expectedErr := boerrors.NewBuildOpError(
				boerrors.EPaCSecretInvalid, fmt.Errorf("something is wrong"))
			waitComponentAnnotationValue(resourceKey,
				PaCProvisionErrorDetailsAnnotationName, expectedErr.ShortError())

			Expect(isCreatePaCPullRequestInvoked).Should(BeFalse())
		})

		It("should successfully submit PR with PaC definitions using token and set PaC annotation", func() {
			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
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
			isSetupPaCWebhookInvoked := false
			SetupPaCWebhookFunc = func(repoUrl string, webhookUrl string, webhookSecret string) error {
				isSetupPaCWebhookInvoked = true
				Expect(webhookUrl).To(Equal(pacWebhookUrl))
				Expect(webhookSecret).ToNot(BeEmpty())
				Expect(repoUrl).To(Equal(SampleRepoLink))
				return nil
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			setComponentDevfileModel(resourceKey)

			waitSecretCreated(namespacePaCSecretKey)
			waitSecretCreated(webhookSecretKey)
			waitPaCRepositoryCreated(resourceKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return isSetupPaCWebhookInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationValue(resourceKey, PaCProvisionAnnotationName, PaCProvisionDoneAnnotationValue)
		})

		It("should provision PaC definitions after initial build if PaC annotation added and be able to switch back to simple build", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("Should not create PaC configuration PR when using simple build")
				return "url", nil
			}

			deleteComponent(resourceKey)

			createComponent(resourceKey)
			setComponentDevfileModel(resourceKey)
			waitOneInitialPipelineRunCreated(resourceKey)
			ensureComponentInitialBuildAnnotationState(resourceKey, true)

			// Switch from simple to advanced workflow

			ResetTestGitProviderClient()

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				return "url", nil
			}
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("Should not create undo PaC configuration PR when switching to PaC build")
				return "url", nil
			}
			SetupPaCWebhookFunc = func(repoUrl string, webhookUrl string, webhookSecret string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			// Add PaC annotation
			component := getComponent(resourceKey)
			if len(component.Annotations) == 0 {
				component.Annotations = make(map[string]string)
			}
			component.Annotations[PaCProvisionAnnotationName] = PaCProvisionRequestedAnnotationValue
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			waitPaCRepositoryCreated(resourceKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationValue(resourceKey, PaCProvisionAnnotationName, PaCProvisionDoneAnnotationValue)
			waitPaCFinalizerOnComponent(resourceKey)

			// Now switch back to simple build

			ResetTestGitProviderClient()

			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isRemovePaCPullRequestInvoked = true
				return "url", nil
			}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("Should not create PaC configuration PR when switching to simple build")
				return "url", nil
			}
			DeletePaCWebhookFunc = func(repoUrl string, webhookUrl string) error {
				defer GinkgoRecover()
				Fail("Should not delete webhook if GitHub application is used")
				return nil
			}

			// Request PaC unprovision
			component = getComponent(resourceKey)
			if len(component.Annotations) == 0 {
				component.Annotations = make(map[string]string)
			}
			component.Annotations[PaCProvisionAnnotationName] = PaCProvisionUnconfigureAnnotationValue
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationGone(resourceKey, PaCProvisionAnnotationName)
			waitPaCFinalizerOnComponentGone(resourceKey)

			// Request simple build
			deleteComponentPipelineRuns(resourceKey)
			component = getComponent(resourceKey)
			if len(component.Annotations) != 0 {
				delete(component.Annotations, InitialBuildAnnotationName)
			}
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			waitOneInitialPipelineRunCreated(resourceKey)
			ensureComponentInitialBuildAnnotationState(resourceKey, true)
		})

		It("should not copy PaC secret into local namespace if GitHub application is used", func() {
			deleteSecret(namespacePaCSecretKey)

			setComponentDevfileModel(resourceKey)

			waitPaCRepositoryCreated(resourceKey)

			ensureSecretNotCreated(namespacePaCSecretKey)
		})

		It("should reuse the same webhook secret for multicomponent repository", func() {
			var webhookSecretStrings []string
			SetupPaCWebhookFunc = func(repoUrl string, webhookUrl string, webhookSecret string) error {
				webhookSecretStrings = append(webhookSecretStrings, webhookSecret)
				return nil
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			component1Key := resourceKey

			component2Key := types.NamespacedName{Name: "component2", Namespace: HASAppNamespace}
			component2 := getSampleComponentData(component2Key)
			component2.Annotations = map[string]string{
				PaCProvisionAnnotationName: PaCProvisionRequestedAnnotationValue,
			}
			component2.Spec.ContainerImage = "registry.io/username/image2:tag2"
			Expect(k8sClient.Create(ctx, component2)).Should(Succeed())
			defer deleteComponent(component2Key)

			setComponentDevfileModel(component1Key)
			setComponentDevfileModel(component2Key)

			waitSecretCreated(namespacePaCSecretKey)
			waitSecretCreated(webhookSecretKey)

			waitPaCRepositoryCreated(component1Key)
			waitPaCRepositoryCreated(component2Key)

			waitComponentAnnotationValue(component1Key, PaCProvisionAnnotationName, PaCProvisionDoneAnnotationValue)
			waitComponentAnnotationValue(component2Key, PaCProvisionAnnotationName, PaCProvisionDoneAnnotationValue)

			Expect(len(webhookSecretStrings) > 0).To(BeTrue())
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

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			component1Key := resourceKey

			component2Key := types.NamespacedName{Name: "component2", Namespace: HASAppNamespace}
			component2 := getSampleComponentData(component2Key)
			component2.Annotations = map[string]string{
				PaCProvisionAnnotationName: PaCProvisionRequestedAnnotationValue,
			}
			component2.Spec.ContainerImage = "registry.io/username/image2:tag2"
			component2.Spec.Source.GitSource.URL = "https://github.com/devfile-samples/devfile-sample-go-basic"
			Expect(k8sClient.Create(ctx, component2)).Should(Succeed())
			defer deleteComponent(component2Key)

			setComponentDevfileModel(component1Key)
			setComponentDevfileModel(component2Key)

			waitSecretCreated(namespacePaCSecretKey)
			waitSecretCreated(webhookSecretKey)

			waitPaCRepositoryCreated(component1Key)
			waitPaCRepositoryCreated(component2Key)

			waitComponentAnnotationValue(component1Key, PaCProvisionAnnotationName, PaCProvisionDoneAnnotationValue)
			waitComponentAnnotationValue(component2Key, PaCProvisionAnnotationName, PaCProvisionDoneAnnotationValue)

			Expect(len(webhookSecretStrings)).To(Equal(2))
			Expect(webhookSecretStrings[0]).ToNot(Equal(webhookSecretStrings[1]))
		})

		It("should not set PaC annotation if PaC definitions merge request submission failed", func() {
			EnsurePaCMergeRequestFunc = func(string, *gp.MergeRequestData) (string, error) {
				return "", fmt.Errorf("Failed to submit PaC definitions PR")
			}

			setComponentDevfileModel(resourceKey)

			waitPaCRepositoryCreated(resourceKey)

			ensureComponentAnnotationValue(resourceKey, PaCProvisionAnnotationName, PaCProvisionRequestedAnnotationValue)

			// Clean up after the test
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return "", nil
			}
			deleteComponent(resourceKey)
			// Wait a bit to not to spoil the next tests.
			// The reason for this is that the test operator could be in the middle of reconcile loop
			// and it's needed to wait until execution reaches the end of the reconcile loop.
			time.Sleep(time.Second)
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

			setComponentDevfileModel(resourceKey)

			Eventually(func() bool {
				return isCreateGithubClientInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationValue(resourceKey, PaCProvisionAnnotationName, PaCProvisionErrorAnnotationValue)
			waitComponentAnnotationValue(resourceKey, PaCProvisionErrorDetailsAnnotationName, appNotInstalledErr.ShortError())

			// Ensure no more retries after permanent error
			gpf.CreateGitClient = func(gitClientConfig gpf.GitClientConfig) (gp.GitProviderClient, error) {
				defer GinkgoRecover()
				Fail("Should not retry PaC provision on permanent error")
				return nil, nil
			}
			ensureComponentAnnotationValue(resourceKey, PaCProvisionAnnotationName, PaCProvisionErrorAnnotationValue)

			// Suppose PaC GH App is installed
			gpf.CreateGitClient = func(gitClientConfig gpf.GitClientConfig) (gp.GitProviderClient, error) {
				return testGitProviderClient, nil
			}
			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				return "url", nil
			}
			// Update PaC annotation to retry
			component := getComponent(resourceKey)
			component.Annotations[PaCProvisionAnnotationName] = PaCProvisionRequestedAnnotationValue
			Expect(k8sClient.Update(ctx, component)).Should(Succeed())

			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
			waitComponentAnnotationValue(resourceKey, PaCProvisionAnnotationName, PaCProvisionDoneAnnotationValue)
		})

		It("should not submit PaC definitions merge request if PaC secret is missing", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			deleteSecret(pacSecretKey)
			deleteSecret(namespacePaCSecretKey)

			setComponentDevfileModel(resourceKey)

			waitComponentAnnotationValue(resourceKey, PaCProvisionAnnotationName, "error")
		})

		It("should do nothing if the component devfile model is not set", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			ensureComponentInitialBuildAnnotationState(resourceKey, false)
		})

		It("should do nothing if initial build annotation is already set and PaC is not used", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			component := getComponent(resourceKey)
			component.Annotations = make(map[string]string)
			component.Annotations[InitialBuildAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).Should(Succeed())

			setComponentDevfileModel(resourceKey)

			time.Sleep(ensureTimeout)
		})

		It("should do nothing if a container image source is specified in component", func() {
			EnsurePaCMergeRequestFunc = func(string, *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			deleteComponent(resourceKey)
			component := &appstudiov1alpha1.Component{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      HASCompName,
					Namespace: HASAppNamespace,
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName:  HASCompName,
					Application:    HASAppName,
					ContainerImage: "quay.io/test/image:latest",
				},
			}
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())

			setComponentDevfileModel(resourceKey)

			ensureComponentInitialBuildAnnotationState(resourceKey, false)
		})

		It("should set default branch as base branch properly when Revision is not set", func() {
			deleteComponent(resourceKey)

			sampleComponent := getSampleComponentData(resourceKey)
			// Unset Revision so that GetDefaultBranch function is called to use the default branch
			// set in the remote component repository
			sampleComponent.Spec.Source.GitSource.Revision = ""
			createComponentForPaCBuild(sampleComponent)

			const repoDefaultBranch = "cool-feature"

			GetDefaultBranchFunc = func(repoUrl string) (string, error) {
				return repoDefaultBranch, nil
			}

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				Expect(d.BaseBranchName).To(Equal(repoDefaultBranch))
				for _, file := range d.Files {
					var prYaml tektonapi.PipelineRun
					if err := yaml.Unmarshal(file.Content, &prYaml); err != nil {
						return "", err
					}
					targetBranches := prYaml.Annotations["pipelinesascode.tekton.dev/on-target-branch"]
					Expect(targetBranches).To(Equal(fmt.Sprintf("[%s]", repoDefaultBranch)))
				}
				isCreatePaCPullRequestInvoked = true
				return "url", nil
			}

			setComponentDevfileModel(resourceKey)

			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
		})

		It("should link auto generated image repository secret to pipeline service accoount", func() {
			deleteComponent(resourceKey)

			userImageRepo := "docker.io/user/image"
			generatedImageRepo := "quay.io/appstudio/generated-image"
			generatedImageRepoSecretName := "generated-image-repo-secret"
			generatedImageRepoSecretKey := types.NamespacedName{Namespace: resourceKey.Namespace, Name: generatedImageRepoSecretName}
			pipelineSAKey := types.NamespacedName{Namespace: resourceKey.Namespace, Name: buildPipelineServiceAccountName}

			checkPROutputImage := func(fileContent []byte, expectedImageRepo string) {
				var prYaml tektonapi.PipelineRun
				Expect(yaml.Unmarshal(fileContent, &prYaml)).To(Succeed())
				outoutImage := ""
				for _, param := range prYaml.Spec.Params {
					if param.Name == "output-image" {
						outoutImage = param.Value.StringVal
						break
					}
				}
				Expect(outoutImage).ToNot(BeEmpty())
				Expect(outoutImage).To(ContainSubstring(expectedImageRepo))
			}

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				checkPROutputImage(d.Files[0].Content, userImageRepo)
				isCreatePaCPullRequestInvoked = true
				return "url", nil
			}

			// Create a component with user's ContainerImage
			component := getSampleComponentData(resourceKey)
			component.Spec.ContainerImage = userImageRepo
			createComponentForPaCBuild(component)
			setComponentDevfileModel(resourceKey)

			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			createSecret(generatedImageRepoSecretKey, nil)
			defer deleteSecret(generatedImageRepoSecretKey)

			// Switch to generated image repository

			component = getComponent(resourceKey)
			component.Annotations[ImageRepoGenerateAnnotationName] = "false"
			component.Annotations[ImageRepoAnnotationName] =
				fmt.Sprintf("{\"image\":\"%s\",\"secret\":\"%s\"}", generatedImageRepo, generatedImageRepoSecretName)
			component.Spec.ContainerImage = generatedImageRepo
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				component = getComponent(resourceKey)
				return component.Spec.ContainerImage == generatedImageRepo
			}, timeout, interval).Should(BeTrue())

			pipelineSA := &corev1.ServiceAccount{}
			Expect(k8sClient.Get(ctx, pipelineSAKey, pipelineSA)).To(Succeed())
			isImageRegistryGeneratedSecretLinked := false
			if pipelineSA.Secrets == nil {
				time.Sleep(1 * time.Second)
				Expect(k8sClient.Get(ctx, pipelineSAKey, pipelineSA)).To(Succeed())
			}
			for _, secret := range pipelineSA.Secrets {
				if secret.Name == generatedImageRepoSecretName {
					isImageRegistryGeneratedSecretLinked = true
					break
				}
			}
			Expect(isImageRegistryGeneratedSecretLinked).To(BeTrue())
		})
	})

	Context("Test Pipelines as Code build preparation", func() {
		_ = BeforeEach(func() {
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			createNamespace(buildServiceNamespaceName)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			ResetTestGitProviderClient()
		})

		_ = AfterEach(func() {
			deleteComponent(resourceKey)

			deleteSecret(webhookSecretKey)
			deleteSecret(namespacePaCSecretKey)

			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
		})

		It("should successfully submit PR with PaC definitions using GitHub application", func() {
			mergeUrl := "merge-url"

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
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

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)

			waitPaCRepositoryCreated(resourceKey)
			waitPaCFinalizerOnComponent(resourceKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("enabled"))
			Expect(buildStatus.PaC.MergeUrl).To(Equal(mergeUrl))
			Expect(buildStatus.PaC.ConfigurationTime).ToNot(BeEmpty())
			Expect(buildStatus.PaC.ErrId).To(Equal(0))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(""))
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

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)

			expectedErr := boerrors.NewBuildOpError(boerrors.EGitHubAppNotInstalled, fmt.Errorf("something is wrong"))
			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("error"))
			Expect(buildStatus.PaC.ConfigurationTime).To(BeEmpty())
			Expect(buildStatus.PaC.ErrId).To(Equal(expectedErr.GetErrorId()))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(expectedErr.ShortError()))
		})

		It("should fail to submit PR if unknown git provider is used", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "url", nil
			}

			createCustomComponentWithBuildRequest(componentConfig{
				componentKey: resourceKey,
				gitURL:       "https://my-git-instance.com/devfile-samples/devfile-sample-java-springboot-basic",
			}, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)

			expectedErr := boerrors.NewBuildOpError(boerrors.EUnknownGitProvider, fmt.Errorf("unknow git provider"))
			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("error"))
			Expect(buildStatus.PaC.ConfigurationTime).To(BeEmpty())
			Expect(buildStatus.PaC.ErrId).To(Equal(expectedErr.GetErrorId()))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(expectedErr.ShortError()))
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

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)

			expectedErr := boerrors.NewBuildOpError(boerrors.EPaCSecretInvalid, fmt.Errorf("invalid pac secret"))
			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("error"))
			Expect(buildStatus.PaC.ConfigurationTime).To(BeEmpty())
			Expect(buildStatus.PaC.ErrId).To(Equal(expectedErr.GetErrorId()))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(expectedErr.ShortError()))
		})

		It("should fail to submit PR if PaC secret is missing", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			deleteSecret(pacSecretKey)
			deleteSecret(namespacePaCSecretKey)

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)

			expectedErr := boerrors.NewBuildOpError(boerrors.EPaCSecretNotFound, fmt.Errorf("pac secret not found"))
			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("error"))
			Expect(buildStatus.PaC.ConfigurationTime).To(BeEmpty())
			Expect(buildStatus.PaC.ErrId).To(Equal(expectedErr.GetErrorId()))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(expectedErr.ShortError()))
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

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)

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
				buildStatus := readBuildStatus(getComponent(resourceKey))
				Expect(buildStatus).ToNot(BeNil())
				Expect(buildStatus.PaC).ToNot(BeNil())
				Expect(buildStatus.PaC.State).To(Equal("error"))
				Expect(buildStatus.PaC.ConfigurationTime).To(BeEmpty())
				Expect(buildStatus.PaC.ErrId).To(Equal(appNotInstalledErr.GetErrorId()))
				Expect(buildStatus.PaC.ErrMessage).To(Equal(appNotInstalledErr.ShortError()))
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
			setComponentBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)

			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("enabled"))
			Expect(buildStatus.PaC.ConfigurationTime).ToNot(BeEmpty())
			Expect(buildStatus.PaC.MergeUrl).To(Equal(mergeUrl))
			Expect(buildStatus.PaC.ErrId).To(Equal(0))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(""))
		})

		It("should not copy PaC secret into local namespace if GitHub application is used", func() {
			deleteSecret(namespacePaCSecretKey)

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)
			waitPaCRepositoryCreated(resourceKey)

			ensureSecretNotCreated(namespacePaCSecretKey)
		})

		It("should successfully submit PR with PaC definitions using token", func() {
			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
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
			isSetupPaCWebhookInvoked := false
			SetupPaCWebhookFunc = func(repoUrl string, webhookUrl string, webhookSecret string) error {
				isSetupPaCWebhookInvoked = true
				Expect(webhookUrl).To(Equal(pacWebhookUrl))
				Expect(webhookSecret).ToNot(BeEmpty())
				Expect(repoUrl).To(Equal(SampleRepoLink))
				return nil
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)

			waitSecretCreated(namespacePaCSecretKey)
			waitSecretCreated(webhookSecretKey)
			waitPaCRepositoryCreated(resourceKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return isSetupPaCWebhookInvoked
			}, timeout, interval).Should(BeTrue())
		})

		It("should provision PaC definitions after initial build, use simple build while PaC enabled, and be able to switch back to simple build only", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("Should not create PaC configuration PR when using simple build")
				return "url", nil
			}

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestTriggerSimpleBuildAnnotationValue)

			waitOneInitialPipelineRunCreated(resourceKey)

			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.Simple).ToNot(BeNil())
			Expect(buildStatus.Simple.BuildStartTime).ToNot(BeEmpty())
			Expect(buildStatus.Simple.ErrId).To(Equal(0))
			Expect(buildStatus.Simple.ErrMessage).To(Equal(""))

			// Do PaC provision
			ResetTestGitProviderClient()

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isCreatePaCPullRequestInvoked = true
				return "configure-merge-url", nil
			}
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("Should not create undo PaC configuration PR when switching to PaC build")
				return "url", nil
			}
			SetupPaCWebhookFunc = func(repoUrl string, webhookUrl string, webhookSecret string) error {
				defer GinkgoRecover()
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			setComponentBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)

			waitPaCRepositoryCreated(resourceKey)
			waitPaCFinalizerOnComponent(resourceKey)
			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			buildStatus = readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("enabled"))
			Expect(buildStatus.PaC.ConfigurationTime).ToNot(BeEmpty())
			Expect(buildStatus.PaC.MergeUrl).To(Equal("configure-merge-url"))
			Expect(buildStatus.PaC.ErrId).To(Equal(0))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(""))

			// Request simple build while PaC is enabled
			deleteComponentPipelineRuns(resourceKey)

			setComponentBuildRequest(resourceKey, BuildRequestTriggerSimpleBuildAnnotationValue)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)

			waitOneInitialPipelineRunCreated(resourceKey)

			buildStatus = readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.Simple).ToNot(BeNil())
			Expect(buildStatus.Simple.BuildStartTime).ToNot(BeEmpty())
			Expect(buildStatus.Simple.ErrId).To(Equal(0))
			Expect(buildStatus.Simple.ErrMessage).To(Equal(""))

			// Do PaC unprovision
			ResetTestGitProviderClient()

			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				isRemovePaCPullRequestInvoked = true
				return "unconfigure-merge-url", nil
			}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("Should not create PaC configuration PR when switching to simple build")
				return "url", nil
			}
			DeletePaCWebhookFunc = func(repoUrl string, webhookUrl string) error {
				defer GinkgoRecover()
				Fail("Should not delete webhook if GitHub application is used")
				return nil
			}

			// Request PaC unprovision
			setComponentBuildRequest(resourceKey, BuildRequestUnconfigurePaCAnnotationValue)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)

			waitPaCFinalizerOnComponentGone(resourceKey)
			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			buildStatus = readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("disabled"))
			Expect(buildStatus.PaC.MergeUrl).To(Equal("unconfigure-merge-url"))
			Expect(buildStatus.PaC.ErrId).To(Equal(0))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(""))

			// Request simple build
			deleteComponentPipelineRuns(resourceKey)

			setComponentBuildRequest(resourceKey, BuildRequestTriggerSimpleBuildAnnotationValue)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)

			waitOneInitialPipelineRunCreated(resourceKey)

			buildStatus = readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.Simple).ToNot(BeNil())
			Expect(buildStatus.Simple.BuildStartTime).ToNot(BeEmpty())
			Expect(buildStatus.Simple.ErrId).To(Equal(0))
			Expect(buildStatus.Simple.ErrMessage).To(Equal(""))
		})

		It("should reuse the same webhook secret for multi component repository", func() {
			var webhookSecretStrings []string
			SetupPaCWebhookFunc = func(repoUrl string, webhookUrl string, webhookSecret string) error {
				webhookSecretStrings = append(webhookSecretStrings, webhookSecret)
				return nil
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			component1Key := resourceKey
			component2Key := types.NamespacedName{Name: "component2", Namespace: HASAppNamespace}

			createComponentWithBuildRequest(component1Key, BuildRequestConfigurePaCAnnotationValue)
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey:   component2Key,
				containerImage: "registry.io/username/image2:tag2",
			}, BuildRequestConfigurePaCAnnotationValue)
			defer deleteComponent(component2Key)

			waitSecretCreated(namespacePaCSecretKey)
			waitSecretCreated(webhookSecretKey)

			waitPaCRepositoryCreated(component1Key)
			waitPaCRepositoryCreated(component2Key)

			waitComponentAnnotationGone(component1Key, BuildRequestAnnotationName)
			waitComponentAnnotationGone(component2Key, BuildRequestAnnotationName)

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

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			component1Key := resourceKey
			component2Key := types.NamespacedName{Name: "component2", Namespace: HASAppNamespace}

			createComponentWithBuildRequest(component1Key, BuildRequestConfigurePaCAnnotationValue)
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey:   component2Key,
				containerImage: "registry.io/username/image2:tag2",
				gitURL:         "https://github.com/devfile-samples/devfile-sample-go-basic",
			}, BuildRequestConfigurePaCAnnotationValue)
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
				componentKey: resourceKey,
				annotations: map[string]string{
					InitialBuildAnnotationName: "processed",
				},
			}, "non-existing-build-request")
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)

			// createComponentAndProcessBuildRequest(resourceKey, "non-existing-build-request")

			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.Message).To(ContainSubstring("unexpected build request"))
		})

		It("should do nothing if the component devfile model is not set", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			createComponent(resourceKey)
			setComponentBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)

			ensureComponentAnnotationValue(resourceKey, BuildRequestAnnotationName, BuildRequestConfigurePaCAnnotationValue)
		})

		It("should do nothing if a container image source is specified in component", func() {
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
					Name:      HASCompName,
					Namespace: HASAppNamespace,
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName:  HASCompName,
					Application:    HASAppName,
					ContainerImage: "quay.io/test/image:latest",
				},
			}
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())

			setComponentBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)

			ensureNoPipelineRunsCreated(resourceKey)
		})

		It("should set default branch as base branch when Revision is not set", func() {
			const repoDefaultBranch = "default-branch"
			GetDefaultBranchFunc = func(repoUrl string) (string, error) {
				return repoDefaultBranch, nil
			}

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				Expect(d.BaseBranchName).To(Equal(repoDefaultBranch))
				for _, file := range d.Files {
					var prYaml tektonapi.PipelineRun
					if err := yaml.Unmarshal(file.Content, &prYaml); err != nil {
						return "", err
					}
					targetBranches := prYaml.Annotations["pipelinesascode.tekton.dev/on-target-branch"]
					Expect(targetBranches).To(Equal(fmt.Sprintf("[%s]", repoDefaultBranch)))
				}
				isCreatePaCPullRequestInvoked = true
				return "url", nil
			}

			component := getSampleComponentData(resourceKey)
			// Unset Revision so that GetDefaultBranch function is called to use the default branch
			// set in the remote component repository
			component.Spec.Source.GitSource.Revision = ""
			component.Annotations[InitialBuildAnnotationName] = "processed"
			component.Annotations[BuildRequestAnnotationName] = BuildRequestConfigurePaCAnnotationValue
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())
			setComponentDevfileModel(resourceKey)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)

			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
		})

		It("should link auto generated image repository secret to pipeline service accoount", func() {
			userImageRepo := "docker.io/user/image"
			generatedImageRepo := "quay.io/appstudio/generated-image"
			generatedImageRepoSecretName := "generated-image-repo-secret"
			generatedImageRepoSecretKey := types.NamespacedName{Namespace: resourceKey.Namespace, Name: generatedImageRepoSecretName}
			pipelineSAKey := types.NamespacedName{Namespace: resourceKey.Namespace, Name: buildPipelineServiceAccountName}

			checkPROutputImage := func(fileContent []byte, expectedImageRepo string) {
				var prYaml tektonapi.PipelineRun
				Expect(yaml.Unmarshal(fileContent, &prYaml)).To(Succeed())
				outoutImage := ""
				for _, param := range prYaml.Spec.Params {
					if param.Name == "output-image" {
						outoutImage = param.Value.StringVal
						break
					}
				}
				Expect(outoutImage).ToNot(BeEmpty())
				Expect(outoutImage).To(ContainSubstring(expectedImageRepo))
			}

			isCreatePaCPullRequestInvoked := false
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				checkPROutputImage(d.Files[0].Content, userImageRepo)
				isCreatePaCPullRequestInvoked = true
				return "url", nil
			}

			// Create a component with user's ContainerImage
			createCustomComponentWithBuildRequest(componentConfig{
				componentKey:   resourceKey,
				containerImage: userImageRepo,
			}, BuildRequestConfigurePaCAnnotationValue)

			Eventually(func() bool {
				return isCreatePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			// Switch to generated image repository
			createSecret(generatedImageRepoSecretKey, nil)
			defer deleteSecret(generatedImageRepoSecretKey)

			component := getComponent(resourceKey)
			component.Annotations[ImageRepoGenerateAnnotationName] = "false"
			component.Annotations[ImageRepoAnnotationName] =
				fmt.Sprintf("{\"image\":\"%s\",\"secret\":\"%s\"}", generatedImageRepo, generatedImageRepoSecretName)
			component.Spec.ContainerImage = generatedImageRepo
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				component = getComponent(resourceKey)
				return component.Spec.ContainerImage == generatedImageRepo
			}, timeout, interval).Should(BeTrue())

			pipelineSA := &corev1.ServiceAccount{}
			Expect(k8sClient.Get(ctx, pipelineSAKey, pipelineSA)).To(Succeed())
			isImageRegistryGeneratedSecretLinked := false
			if pipelineSA.Secrets == nil {
				time.Sleep(1 * time.Second)
				Expect(k8sClient.Get(ctx, pipelineSAKey, pipelineSA)).To(Succeed())
			}
			for _, secret := range pipelineSA.Secrets {
				if secret.Name == generatedImageRepoSecretName {
					isImageRegistryGeneratedSecretLinked = true
					break
				}
			}
			Expect(isImageRegistryGeneratedSecretLinked).To(BeTrue())
		})
	})

	Context("Test Pipelines as Code build clean up [deprecated annotations]", func() {

		_ = BeforeEach(func() {
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			createNamespace(buildServiceNamespaceName)

			ResetTestGitProviderClient()

			deleteComponent(resourceKey)
		})

		_ = AfterEach(func() {
			deleteSecret(webhookSecretKey)
			deleteSecret(namespacePaCSecretKey)

			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
		})

		It("should successfully submit PR with PaC definitions removal using GitHub application", func() {
			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				isRemovePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
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

			createComponentForPaCBuild(getSampleComponentData(resourceKey))
			setComponentDevfileModel(resourceKey)
			waitPaCFinalizerOnComponent(resourceKey)

			deleteComponent(resourceKey)

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
		})

		It("should successfully submit merge request with PaC definitions removal using token", func() {
			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				isRemovePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
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
			isDeletePaCWebhookInvoked := false
			DeletePaCWebhookFunc = func(repoUrl string, webhookUrl string) error {
				isDeletePaCWebhookInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
				Expect(webhookUrl).To(Equal(pacWebhookUrl))
				return nil
			}
			isDeleteBranchInvoked := false
			DeleteBranchFunc = func(repoUrl string, branchName string) (bool, error) {
				isDeleteBranchInvoked = true
				return true, nil
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			createComponentForPaCBuild(getSampleComponentData(resourceKey))
			setComponentDevfileModel(resourceKey)
			waitPaCFinalizerOnComponent(resourceKey)

			deleteComponent(resourceKey)

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return isDeletePaCWebhookInvoked
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return isDeleteBranchInvoked
			}, timeout, interval).Should(BeFalse())
		})

		It("should not block component deletion if PaC definitions removal failed", func() {
			UndoPaCMergeRequestFunc = func(string, *gp.MergeRequestData) (webUrl string, err error) {
				return "", fmt.Errorf("failed to create PR")
			}
			DeletePaCWebhookFunc = func(string, string) error {
				return fmt.Errorf("failed to delete webhook")
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			createComponentForPaCBuild(getSampleComponentData(resourceKey))
			setComponentDevfileModel(resourceKey)
			waitPaCFinalizerOnComponent(resourceKey)

			// deleteComponent waits until the component is gone
			deleteComponent(resourceKey)
		})

		var assertCloseUnmergedMergeRequest = func(expectedBaseBranch string, sourceBranchExists bool) {
			UndoPaCMergeRequestFunc = func(string, *gp.MergeRequestData) (webUrl string, err error) {
				defer GinkgoRecover()
				Fail("Expect to close unmerged merge request other than delete .tekton/")
				return "", nil
			}

			component := getSampleComponentData(resourceKey)
			if expectedBaseBranch == "" {
				expectedBaseBranch = component.Spec.Source.GitSource.Revision
			} else {
				component.Spec.Source.GitSource.Revision = ""
			}

			expectedSourceBranch := pacMergeRequestSourceBranchPrefix + component.Name

			isFindOnboardingMergeRequestInvoked := false
			FindUnmergedPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (*gp.MergeRequest, error) {
				isFindOnboardingMergeRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
				Expect(d.BranchName).Should(Equal(expectedSourceBranch))
				Expect(d.BaseBranchName).Should(Equal(expectedBaseBranch))
				return &gp.MergeRequest{
					WebUrl: "url",
				}, nil
			}

			isDeleteBranchInvoked := false
			DeleteBranchFunc = func(repoUrl string, branchName string) (bool, error) {
				isDeleteBranchInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
				Expect(branchName).Should(Equal(expectedSourceBranch))
				return sourceBranchExists, nil
			}

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			createComponentForPaCBuild(component)
			setComponentDevfileModel(resourceKey)
			waitPaCFinalizerOnComponent(resourceKey)

			deleteComponent(resourceKey)

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
			GetDefaultBranchFunc = func(repoUrl string) (string, error) {
				return defaultBranch, nil
			}
			assertCloseUnmergedMergeRequest(defaultBranch, true)
		})
	})

	Context("Test Pipelines as Code build clean up", func() {

		_ = BeforeEach(func() {
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			createNamespace(buildServiceNamespaceName)

			ResetTestGitProviderClient()

			deleteComponent(resourceKey)
		})

		_ = AfterEach(func() {
			deleteSecret(webhookSecretKey)
			deleteSecret(namespacePaCSecretKey)

			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
		})

		It("should successfully submit PR with PaC definitions removal using GitHub application", func() {
			mergeUrl := "merge-url"
			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				isRemovePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
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

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceKey)

			setComponentBuildRequest(resourceKey, BuildRequestUnconfigurePaCAnnotationValue)

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())

			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("disabled"))
			Expect(buildStatus.PaC.MergeUrl).To(Equal(mergeUrl))
			Expect(buildStatus.PaC.ErrId).To(Equal(0))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(""))
		})

		It("should successfully submit PR with PaC definitions removal on component deletion", func() {
			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				isRemovePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
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

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceKey)

			deleteComponent(resourceKey)

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
		})

		It("should successfully submit merge request with PaC definitions removal using token", func() {
			mergeUrl := "merge-url"
			isRemovePaCPullRequestInvoked := false
			UndoPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (webUrl string, err error) {
				isRemovePaCPullRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
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
			isDeletePaCWebhookInvoked := false
			DeletePaCWebhookFunc = func(repoUrl string, webhookUrl string) error {
				isDeletePaCWebhookInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
				Expect(webhookUrl).To(Equal(pacWebhookUrl))
				return nil
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceKey)

			setComponentBuildRequest(resourceKey, BuildRequestUnconfigurePaCAnnotationValue)

			Eventually(func() bool {
				return isRemovePaCPullRequestInvoked
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return isDeletePaCWebhookInvoked
			}, timeout, interval).Should(BeTrue())

			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.PaC).ToNot(BeNil())
			Expect(buildStatus.PaC.State).To(Equal("disabled"))
			Expect(buildStatus.PaC.MergeUrl).To(Equal(mergeUrl))
			Expect(buildStatus.PaC.ErrId).To(Equal(0))
			Expect(buildStatus.PaC.ErrMessage).To(Equal(""))
		})

		It("should not block component deletion if PaC definitions removal failed", func() {
			UndoPaCMergeRequestFunc = func(string, *gp.MergeRequestData) (webUrl string, err error) {
				return "", fmt.Errorf("failed to create PR")
			}
			DeletePaCWebhookFunc = func(string, string) error {
				return fmt.Errorf("failed to delete webhook")
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			createComponentAndProcessBuildRequest(resourceKey, BuildRequestConfigurePaCAnnotationValue)
			waitPaCFinalizerOnComponent(resourceKey)

			// deleteComponent waits until the component is gone
			deleteComponent(resourceKey)
		})

		var assertCloseUnmergedMergeRequest = func(expectedBaseBranch string, sourceBranchExists bool) {
			UndoPaCMergeRequestFunc = func(string, *gp.MergeRequestData) (webUrl string, err error) {
				defer GinkgoRecover()
				Fail("Should close unmerged PaC configuration merge request instead of deleting .tekton/ directory")
				return "", nil
			}

			component := getSampleComponentData(resourceKey)
			if expectedBaseBranch == "" {
				expectedBaseBranch = component.Spec.Source.GitSource.Revision
			} else {
				component.Spec.Source.GitSource.Revision = ""
			}
			component.Annotations[InitialBuildAnnotationName] = "processed"
			component.Annotations[BuildRequestAnnotationName] = BuildRequestConfigurePaCAnnotationValue

			expectedSourceBranch := pacMergeRequestSourceBranchPrefix + component.Name

			isFindOnboardingMergeRequestInvoked := false
			FindUnmergedPaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (*gp.MergeRequest, error) {
				isFindOnboardingMergeRequestInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
				Expect(d.BranchName).Should(Equal(expectedSourceBranch))
				Expect(d.BaseBranchName).Should(Equal(expectedBaseBranch))
				return &gp.MergeRequest{
					WebUrl: "url",
				}, nil
			}

			isDeleteBranchInvoked := false
			DeleteBranchFunc = func(repoUrl string, branchName string) (bool, error) {
				isDeleteBranchInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
				Expect(branchName).Should(Equal(expectedSourceBranch))
				return sourceBranchExists, nil
			}

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			Expect(k8sClient.Create(ctx, component)).Should(Succeed())
			setComponentDevfileModel(resourceKey)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)
			waitPaCFinalizerOnComponent(resourceKey)

			deleteComponent(resourceKey)

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
			GetDefaultBranchFunc = func(repoUrl string) (string, error) {
				return defaultBranch, nil
			}
			assertCloseUnmergedMergeRequest(defaultBranch, true)
		})
	})

	Context("Test initial build", func() {

		_ = BeforeEach(func() {
			createNamespace(buildServiceNamespaceName)
			ResetTestGitProviderClient()

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			createComponent(resourceKey)
		})

		_ = AfterEach(func() {
			deleteSecret(pacSecretKey)

			deleteBuildPipelineRunSelector(defaultSelectorKey)
			deleteComponentPipelineRuns(resourceKey)
			deleteComponent(resourceKey)
		})

		It("should submit initial build on component creation", func() {
			gitSourceSHA := "d1a9e858489d1515621398fb02942da068f1c956"

			isGetBranchShaInvoked := false
			GetBranchShaFunc = func(repoUrl string, branchName string) (string, error) {
				isGetBranchShaInvoked = true
				Expect(repoUrl).To(Equal(SampleRepoLink))
				return gitSourceSHA, nil
			}
			GetBrowseRepositoryAtShaLinkFunc = func(repoUrl, sha string) string {
				Expect(repoUrl).To(Equal(SampleRepoLink))
				Expect(sha).To(Equal(gitSourceSHA))
				return "https://github.com/devfile-samples/devfile-sample-java-springboot-basic?rev=" + gitSourceSHA
			}

			setComponentDevfileModel(resourceKey)

			Eventually(func() bool {
				return isGetBranchShaInvoked
			}, timeout, interval).Should(BeTrue())

			waitOneInitialPipelineRunCreated(resourceKey)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)
			ensureComponentInitialBuildAnnotationState(resourceKey, true)

			// Check pipeline run labels and annotations
			pipelineRun := listComponentPipelineRuns(resourceKey)[0]
			Expect(pipelineRun.Annotations[gitCommitShaAnnotationName]).To(Equal(gitSourceSHA))
			Expect(pipelineRun.Annotations[gitRepoAtShaAnnotationName]).To(
				Equal("https://github.com/devfile-samples/devfile-sample-java-springboot-basic?rev=" + gitSourceSHA))
		})

		It("should be able to retrigger simple build", func() {
			setComponentDevfileModel(resourceKey)

			waitOneInitialPipelineRunCreated(resourceKey)
			ensureComponentInitialBuildAnnotationState(resourceKey, true)

			deleteComponentPipelineRuns(resourceKey)

			setComponentBuildRequest(resourceKey, BuildRequestTriggerSimpleBuildAnnotationValue)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)
			waitOneInitialPipelineRunCreated(resourceKey)
			ensureComponentInitialBuildAnnotationState(resourceKey, true)
		})

		It("should submit initial build if retrieving of git commit SHA failed", func() {
			isGetBranchShaInvoked := false
			GetBranchShaFunc = func(repoUrl string, branchName string) (string, error) {
				isGetBranchShaInvoked = true
				return "", fmt.Errorf("failed to get git commit SHA")
			}

			setComponentDevfileModel(resourceKey)

			Eventually(func() bool {
				return isGetBranchShaInvoked
			}, timeout, interval).Should(BeTrue())

			waitOneInitialPipelineRunCreated(resourceKey)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)
			ensureComponentInitialBuildAnnotationState(resourceKey, true)
		})

		It("should submit initial build for private git repository", func() {
			gitSecretName := "git-secret"

			isGetBranchShaInvoked := false
			GetBranchShaFunc = func(repoUrl string, branchName string) (string, error) {
				isGetBranchShaInvoked = true
				return "", fmt.Errorf("failed to get git commit SHA")
			}

			spiAccessTokenBinding := &appstudiospiapiv1beta1.SPIAccessTokenBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "spi-access-token-binding",
					Namespace: resourceKey.Namespace,
				},
				Spec: appstudiospiapiv1beta1.SPIAccessTokenBindingSpec{
					RepoUrl: SampleRepoLink,
				},
			}
			Expect(k8sClient.Create(ctx, spiAccessTokenBinding)).To(Succeed())
			spiAccessTokenBinding.Status.SyncedObjectRef.Name = gitSecretName
			Expect(k8sClient.Status().Update(ctx, spiAccessTokenBinding)).To(Succeed())

			setComponentDevfileModel(resourceKey)

			Eventually(func() bool { return isGetBranchShaInvoked }, timeout, interval).Should(BeTrue())

			// Wait until all resources created
			waitOneInitialPipelineRunCreated(resourceKey)
			waitComponentAnnotationGone(resourceKey, BuildRequestAnnotationName)
			ensureComponentInitialBuildAnnotationState(resourceKey, true)

			// Check the pipeline run and its resources
			pipelineRuns := listComponentPipelineRuns(resourceKey)
			Expect(len(pipelineRuns)).To(Equal(1))
			pipelineRun := pipelineRuns[0]

			Expect(pipelineRun.Labels[ApplicationNameLabelName]).To(Equal(HASAppName))
			Expect(pipelineRun.Labels[ComponentNameLabelName]).To(Equal(HASCompName))

			Expect(pipelineRun.Annotations["build.appstudio.redhat.com/pipeline_name"]).To(Equal(defaultPipelineName))
			Expect(pipelineRun.Annotations["build.appstudio.redhat.com/bundle"]).To(Equal(defaultPipelineBundle))

			Expect(pipelineRun.Spec.PipelineSpec).To(BeNil())

			Expect(pipelineRun.Spec.PipelineRef).ToNot(BeNil())
			Expect(pipelineRun.Spec.PipelineRef.Name).To(Equal(defaultPipelineName))
			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(defaultPipelineBundle))

			Expect(pipelineRun.Spec.Params).ToNot(BeEmpty())
			for _, p := range pipelineRun.Spec.Params {
				switch p.Name {
				case "output-image":
					Expect(p.Value.StringVal).ToNot(BeEmpty())
					Expect(strings.HasPrefix(p.Value.StringVal, "docker.io/foo/customized:"+HASCompName+"-build-"))
				case "git-url":
					Expect(p.Value.StringVal).To(Equal(SampleRepoLink))
				case "revision":
					Expect(p.Value.StringVal).To(Equal("main"))
				}
			}

			Expect(pipelineRun.Spec.Workspaces).To(Not(BeEmpty()))
			for _, w := range pipelineRun.Spec.Workspaces {
				if w.Name == "workspace" {
					Expect(w.VolumeClaimTemplate).NotTo(
						Equal(nil), "PipelineRun should have its own volumeClaimTemplate.")
				}
				if w.Name == "git-auth" {
					Expect(w.Secret.SecretName).To(Equal(gitSecretName))
				}
			}

			k8sClient.Delete(ctx, spiAccessTokenBinding)
		})

		It("should fail to submit initial build for private git repository if SPIAccessTokenBinding is missing", func() {
			isRepositoryPublicInvoked := false
			IsRepositoryPublicFunc = func(repoUrl string) (bool, error) {
				isRepositoryPublicInvoked = true
				return false, nil
			}

			setComponentDevfileModel(resourceKey)

			Eventually(func() bool { return isRepositoryPublicInvoked }, timeout, interval).Should(BeTrue())

			buildStatus := readBuildStatus(getComponent(resourceKey))
			Expect(buildStatus).ToNot(BeNil())
			Expect(buildStatus.Simple).ToNot(BeNil())
			Expect(buildStatus.Simple.ErrId).To(Equal(int(boerrors.EComponentGitSecretMissing)))
		})

		It("should not submit initial build if the component devfile model is not set", func() {
			ensureNoPipelineRunsCreated(resourceKey)
		})

		It("should do nothing if simple build already happened (and PaC is not used)", func() {
			deleteComponent(resourceKey)

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("PR creation should not be invoked")
				return "", nil
			}

			component := getSampleComponentData(resourceKey)
			component.Annotations = make(map[string]string)
			component.Annotations[BuildStatusAnnotationName] = "{simple:{\"build-start-time\": \"time\"}}"
			component.Annotations[InitialBuildAnnotationName] = "processed"
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())

			ensureNoPipelineRunsCreated(resourceKey)
		})

		It("should not submit initial build if a container image source is specified in component", func() {
			deleteComponent(resourceKey)

			component := &appstudiov1alpha1.Component{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      HASCompName,
					Namespace: HASAppNamespace,
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName:  HASCompName,
					Application:    HASAppName,
					ContainerImage: "quay.io/test/image:latest",
				},
			}
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())

			setComponentDevfileModel(resourceKey)

			ensureNoPipelineRunsCreated(resourceKey)
		})
	})

	Context("Resolve the correct build bundle during the component creation", func() {

		BeforeEach(func() {
			createNamespace(buildServiceNamespaceName)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			createComponent(resourceKey)
		})

		AfterEach(func() {
			deleteComponent(resourceKey)
			deleteComponentPipelineRuns(resourceKey)
			deleteSecret(pacSecretKey)
		})

		It("should use the build bundle specified for application", func() {
			selectors := &buildappstudiov1alpha1.BuildPipelineSelector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      HASAppName,
					Namespace: HASAppNamespace,
				},
				Spec: buildappstudiov1alpha1.BuildPipelineSelectorSpec{
					Selectors: []buildappstudiov1alpha1.PipelineSelector{
						{
							Name: "nodejs",
							PipelineRef: tektonapi.PipelineRef{
								Name:   "nodejs-builder",
								Bundle: defaultPipelineBundle,
							},
							PipelineParams: []buildappstudiov1alpha1.PipelineParam{
								{
									Name:  "additional-param",
									Value: "additional-param-value-application",
								},
							},
							WhenConditions: buildappstudiov1alpha1.WhenCondition{
								Language: "nodejs",
							},
						},
						{
							Name: "Fallback",
							PipelineRef: tektonapi.PipelineRef{
								Name:   "noop",
								Bundle: defaultPipelineBundle,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, selectors)).To(Succeed())

			devfile := `
                        schemaVersion: 2.2.0
                        metadata:
                            name: devfile-nodejs
                            language: nodejs
                    `
			setComponentDevfile(resourceKey, devfile)

			waitOneInitialPipelineRunCreated(resourceKey)
			pipelineRun := listComponentPipelineRuns(resourceKey)[0]

			Expect(pipelineRun.Labels[ApplicationNameLabelName]).To(Equal(HASAppName))
			Expect(pipelineRun.Labels[ComponentNameLabelName]).To(Equal(HASCompName))

			Expect(pipelineRun.Annotations["build.appstudio.redhat.com/pipeline_name"]).To(Equal("nodejs-builder"))
			Expect(pipelineRun.Annotations["build.appstudio.redhat.com/bundle"]).To(Equal(defaultPipelineBundle))

			Expect(pipelineRun.Spec.PipelineSpec).To(BeNil())

			Expect(pipelineRun.Spec.PipelineRef).ToNot(BeNil())
			Expect(pipelineRun.Spec.PipelineRef.Name).To(Equal("nodejs-builder"))
			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(defaultPipelineBundle))

			Expect(pipelineRun.Spec.Params).ToNot(BeNil())
			additionalPipelineParameterFound := false
			for _, param := range pipelineRun.Spec.Params {
				if param.Name == "additional-param" {
					Expect(param.Value.StringVal).To(Equal("additional-param-value-application"))
					additionalPipelineParameterFound = true
					break
				}
			}
			Expect(additionalPipelineParameterFound).To(BeTrue(), "additional pipeline parameter not found")

			Expect(k8sClient.Delete(ctx, selectors)).Should(Succeed())
		})

		It("should use the build bundle specified for the namespace", func() {
			selectors := &buildappstudiov1alpha1.BuildPipelineSelector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      buildPipelineSelectorResourceName,
					Namespace: HASAppNamespace,
				},
				Spec: buildappstudiov1alpha1.BuildPipelineSelectorSpec{
					Selectors: []buildappstudiov1alpha1.PipelineSelector{
						{
							Name: "nodejs",
							PipelineRef: tektonapi.PipelineRef{
								Name:   "nodejs-builder",
								Bundle: defaultPipelineBundle,
							},
							PipelineParams: []buildappstudiov1alpha1.PipelineParam{
								{
									Name:  "additional-param",
									Value: "additional-param-value-namespace",
								},
							},
							WhenConditions: buildappstudiov1alpha1.WhenCondition{
								Language: "nodejs",
							},
						},
						{
							Name: "Fallback",
							PipelineRef: tektonapi.PipelineRef{
								Name:   "noop",
								Bundle: defaultPipelineBundle,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, selectors)).To(Succeed())

			devfile := `
                        schemaVersion: 2.2.0
                        metadata:
                            name: devfile-nodejs
                            language: nodejs
                    `
			setComponentDevfile(resourceKey, devfile)

			waitOneInitialPipelineRunCreated(resourceKey)
			pipelineRun := listComponentPipelineRuns(resourceKey)[0]

			Expect(pipelineRun.Labels[ApplicationNameLabelName]).To(Equal(HASAppName))
			Expect(pipelineRun.Labels[ComponentNameLabelName]).To(Equal(HASCompName))

			Expect(pipelineRun.Annotations["build.appstudio.redhat.com/pipeline_name"]).To(Equal("nodejs-builder"))
			Expect(pipelineRun.Annotations["build.appstudio.redhat.com/bundle"]).To(Equal(defaultPipelineBundle))

			Expect(pipelineRun.Spec.PipelineSpec).To(BeNil())

			Expect(pipelineRun.Spec.PipelineRef).ToNot(BeNil())
			Expect(pipelineRun.Spec.PipelineRef.Name).To(Equal("nodejs-builder"))
			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(defaultPipelineBundle))

			Expect(pipelineRun.Spec.Params).ToNot(BeNil())
			additionalPipelineParameterFound := false
			for _, param := range pipelineRun.Spec.Params {
				if param.Name == "additional-param" {
					Expect(param.Value.StringVal).To(Equal("additional-param-value-namespace"))
					additionalPipelineParameterFound = true
					break
				}
			}
			Expect(additionalPipelineParameterFound).To(BeTrue(), "additional pipeline parameter not found")

			Expect(k8sClient.Delete(ctx, selectors)).Should(Succeed())
		})

		It("should use the global build bundle", func() {
			deleteBuildPipelineRunSelector(defaultSelectorKey)
			selectors := &buildappstudiov1alpha1.BuildPipelineSelector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      buildPipelineSelectorResourceName,
					Namespace: buildServiceNamespaceName,
				},
				Spec: buildappstudiov1alpha1.BuildPipelineSelectorSpec{
					Selectors: []buildappstudiov1alpha1.PipelineSelector{
						{
							Name: "java",
							PipelineRef: tektonapi.PipelineRef{
								Name:   "java-builder",
								Bundle: defaultPipelineBundle,
							},
							PipelineParams: []buildappstudiov1alpha1.PipelineParam{
								{
									Name:  "additional-param",
									Value: "additional-param-value-global",
								},
							},
							WhenConditions: buildappstudiov1alpha1.WhenCondition{
								Language: "java",
							},
						},
						{
							Name: "Fallback",
							PipelineRef: tektonapi.PipelineRef{
								Name:   "noop",
								Bundle: defaultPipelineBundle,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, selectors)).To(Succeed())

			devfile := `
                        schemaVersion: 2.2.0
                        metadata:
                            name: devfile-java
                            language: java
                    `
			setComponentDevfile(resourceKey, devfile)

			waitOneInitialPipelineRunCreated(resourceKey)
			pipelineRun := listComponentPipelineRuns(resourceKey)[0]

			Expect(pipelineRun.Labels[ApplicationNameLabelName]).To(Equal(HASAppName))
			Expect(pipelineRun.Labels[ComponentNameLabelName]).To(Equal(HASCompName))

			Expect(pipelineRun.Annotations["build.appstudio.redhat.com/pipeline_name"]).To(Equal("java-builder"))
			Expect(pipelineRun.Annotations["build.appstudio.redhat.com/bundle"]).To(Equal(defaultPipelineBundle))

			Expect(pipelineRun.Spec.PipelineSpec).To(BeNil())

			Expect(pipelineRun.Spec.PipelineRef).ToNot(BeNil())
			Expect(pipelineRun.Spec.PipelineRef.Name).To(Equal("java-builder"))
			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(defaultPipelineBundle))

			Expect(pipelineRun.Spec.Params).ToNot(BeNil())
			additionalPipelineParameterFound := false
			for _, param := range pipelineRun.Spec.Params {
				if param.Name == "additional-param" {
					Expect(param.Value.StringVal).To(Equal("additional-param-value-global"))
					additionalPipelineParameterFound = true
					break
				}
			}
			Expect(additionalPipelineParameterFound).To(BeTrue(), "additional pipeline parameter not found")

			Expect(k8sClient.Delete(ctx, selectors)).Should(Succeed())
		})
	})

	Context("Test build pipeline failure if no matched pipeline is selected", func() {
		BeforeEach(func() {
			deleteBuildPipelineRunSelector(defaultSelectorKey)

			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
			ResetTestGitProviderClient()
		})

		AfterEach(func() {
			deleteComponent(resourceKey)
			deleteSecret(pacSecretKey)
		})

		createBuildPipelineSelector := func() {
			selector := &buildappstudiov1alpha1.BuildPipelineSelector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      defaultSelectorKey.Name,
					Namespace: defaultSelectorKey.Namespace,
				},
				Spec: buildappstudiov1alpha1.BuildPipelineSelectorSpec{
					Selectors: []buildappstudiov1alpha1.PipelineSelector{
						{
							Name: "java",
							PipelineRef: tektonapi.PipelineRef{
								Name:   "java-builder",
								Bundle: defaultPipelineBundle,
							},
							PipelineParams: []buildappstudiov1alpha1.PipelineParam{
								{
									Name:  "additional-param",
									Value: "additional-param-value-global",
								},
							},
							WhenConditions: buildappstudiov1alpha1.WhenCondition{
								Language: "java",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, selector)).To(Succeed())
		}

		assertBuildFail := func(doInitialBuild bool, expectedErrMsg string) {
			component := getSampleComponentData(resourceKey)
			if doInitialBuild {
				component.Annotations[BuildRequestAnnotationName] = BuildRequestTriggerSimpleBuildAnnotationValue
			} else {
				component.Annotations[BuildRequestAnnotationName] = BuildRequestConfigurePaCAnnotationValue
			}
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())

			// devfile is about nodejs rather than Java, which causes failure of selecting a pipeline.
			devfile := `
                        schemaVersion: 2.2.0
                        metadata:
                            name: devfile-nodejs
                            language: nodejs
                    `
			setComponentDevfile(resourceKey, devfile)

			ensureNoPipelineRunsCreated(resourceKey)

			component = getComponent(resourceKey)
			if doInitialBuild {
				Expect(component.Annotations[InitialBuildAnnotationName]).To(Equal("processed"))
			}

			// Check expected errors
			statusAnnotation := component.Annotations[BuildStatusAnnotationName]
			Expect(strings.Contains(statusAnnotation, expectedErrMsg)).To(BeTrue())
		}

		It("initial build should fail when Component CR does not match any predefined pipeline", func() {
			createBuildPipelineSelector()
			assertBuildFail(true, "No pipeline is selected")
		})

		It("PaC provision should fail when Component CR does not match any predefined pipeline", func() {
			createBuildPipelineSelector()
			assertBuildFail(false, "No pipeline is selected")
		})

		It("initial build should fail when no BuildPipelineSelector CR is defined", func() {
			assertBuildFail(true, "Build pipeline selector is not defined")
		})

		It("PaC provision should fail when no BuildPipelineSelector CR is defined", func() {
			assertBuildFail(false, "Build pipeline selector is not defined")
		})
	})
})
