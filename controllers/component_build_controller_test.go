/*
Copyright 2021-2022 Red Hat, Inc.

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
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	"github.com/redhat-appstudio/build-service/pkg/github"
	"github.com/redhat-appstudio/build-service/pkg/gitlab"
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
		pacSecretKey          = types.NamespacedName{Name: gitopsprepare.PipelinesAsCodeSecretName, Namespace: pipelinesAsCodeNamespace}
		namespacePaCSecretKey = types.NamespacedName{Name: gitopsprepare.PipelinesAsCodeSecretName, Namespace: HASAppNamespace}
		webhookSecretKey      = types.NamespacedName{Name: gitops.PipelinesAsCodeWebhooksSecretName, Namespace: HASAppNamespace}
	)

	Context("Test Pipelines as Code build preparation", func() {

		_ = BeforeEach(func() {
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac-host")
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)

			github.NewGithubClientByApp = func(appId int64, privateKeyPem []byte, owner string) (*github.GithubClient, error) { return nil, nil }
			github.NewGithubClient = func(accessToken string) *github.GithubClient { return nil }

			createComponentForPaCBuild(resourceKey)
		}, 30)

		_ = AfterEach(func() {
			deleteComponent(resourceKey)

			deleteSecret(webhookSecretKey)
			deleteSecret(namespacePaCSecretKey)

			deleteSecret(pacSecretKey)
			deleteRoute(pacRouteKey)
		}, 30)

		It("should successfully submit PR with PaC definitions using GitHub application and set initial build annotation", func() {
			github.CreatePaCPullRequest = func(c *github.GithubClient, d *github.PaCPullRequestData) (string, error) {
				Expect(d.Owner).To(Equal("devfile-samples"))
				Expect(d.Repository).To(Equal("devfile-sample-java-springboot-basic"))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.Branch).ToNot(BeEmpty())
				Expect(d.BaseBranch).ToNot(BeEmpty())
				Expect(d.PRTitle).ToNot(BeEmpty())
				Expect(d.PRText).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return "url", nil
			}
			github.SetupPaCWebhook = func(g *github.GithubClient, webhookUrl, webhookSecret, owner, repository string) error {
				Fail("Should not create webhook if GitHub application is used")
				return nil
			}

			setComponentDevfileModel(resourceKey)

			ensurePaCRepositoryCreated(resourceKey)

			ensureComponentInitialBuildAnnotationState(resourceKey, true)
		})

		It("should successfully submit PR with PaC definitions using GitHub token and set initial build annotation", func() {
			github.CreatePaCPullRequest = func(c *github.GithubClient, d *github.PaCPullRequestData) (string, error) {
				Expect(d.Owner).To(Equal("devfile-samples"))
				Expect(d.Repository).To(Equal("devfile-sample-java-springboot-basic"))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.Branch).ToNot(BeEmpty())
				Expect(d.BaseBranch).ToNot(BeEmpty())
				Expect(d.PRTitle).ToNot(BeEmpty())
				Expect(d.PRText).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return "url", nil
			}
			github.SetupPaCWebhook = func(g *github.GithubClient, webhookUrl, webhookSecret, owner, repository string) error {
				Expect(webhookUrl).To(Equal(pacWebhookUrl))
				Expect(webhookSecret).ToNot(BeEmpty())
				Expect(owner).To(Equal("devfile-samples"))
				Expect(repository).To(Equal("devfile-sample-java-springboot-basic"))
				return nil
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			setComponentDevfileModel(resourceKey)

			ensureSecretCreated(namespacePaCSecretKey)
			ensureSecretCreated(webhookSecretKey)
			ensurePaCRepositoryCreated(resourceKey)

			ensureComponentInitialBuildAnnotationState(resourceKey, true)
		})

		It("should successfully submit MR with PaC definitions using GitLab token and set initial build annotation", func() {
			gitlab.EnsurePaCMergeRequest = func(c *gitlab.GitlabClient, d *gitlab.PaCMergeRequestData) (string, error) {
				Expect(d.ProjectPath).To(Equal("devfile-samples/devfile-sample-go-basic"))
				Expect(len(d.Files)).To(Equal(2))
				for _, file := range d.Files {
					Expect(strings.HasPrefix(file.FullPath, ".tekton/")).To(BeTrue())
				}
				Expect(d.CommitMessage).ToNot(BeEmpty())
				Expect(d.Branch).ToNot(BeEmpty())
				Expect(d.BaseBranch).ToNot(BeEmpty())
				Expect(d.MrTitle).ToNot(BeEmpty())
				Expect(d.MrText).ToNot(BeEmpty())
				Expect(d.AuthorName).ToNot(BeEmpty())
				Expect(d.AuthorEmail).ToNot(BeEmpty())
				return "url", nil
			}
			gitlab.SetupPaCWebhook = func(g *gitlab.GitlabClient, projectPath, webhookUrl, webhookSecret string) error {
				Expect(webhookUrl).To(Equal(pacWebhookUrl))
				Expect(webhookSecret).ToNot(BeEmpty())
				Expect(projectPath).To(Equal("devfile-samples/devfile-sample-go-basic"))
				return nil
			}

			pacSecretData := map[string]string{"gitlab.token": "glpat-token"}
			createSecret(pacSecretKey, pacSecretData)

			deleteComponent(resourceKey)
			component := &appstudiov1alpha1.Component{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      HASCompName,
					Namespace: HASAppNamespace,
					Annotations: map[string]string{
						gitops.PaCAnnotation: "1",
					},
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName:  HASCompName,
					Application:    HASAppName,
					ContainerImage: ComponentContainerImage,
					Source: appstudiov1alpha1.ComponentSource{
						ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
							GitSource: &appstudiov1alpha1.GitSource{
								URL: "https://gitlab.com/devfile-samples/devfile-sample-go-basic",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())

			setComponentDevfileModel(resourceKey)

			ensureSecretCreated(namespacePaCSecretKey)
			ensureSecretCreated(webhookSecretKey)
			ensurePaCRepositoryCreated(resourceKey)

			ensureComponentInitialBuildAnnotationState(resourceKey, true)
		})

		It("should update PaC secret in local namespace if global one updated", func() {
			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			setComponentDevfileModel(resourceKey)

			ensureSecretCreated(namespacePaCSecretKey)
			ensureSecretCreated(webhookSecretKey)
			ensurePaCRepositoryCreated(resourceKey)

			deleteComponent(resourceKey)

			pacSecretNewData := map[string]string{
				"github.token": "ghp_token22",
				"gitlab.token": "glpat-token22",
			}
			createSecret(pacSecretKey, pacSecretNewData)

			createComponentForPaCBuild(resourceKey)
			setComponentDevfileModel(resourceKey)

			ensureSecretCreated(namespacePaCSecretKey)
			ensureSecretCreated(webhookSecretKey)
			ensurePaCRepositoryCreated(resourceKey)

			Eventually(func() bool {
				secret := &corev1.Secret{}
				err := k8sClient.Get(ctx, pacSecretKey, secret)
				return err == nil && secret.ResourceVersion != "" &&
					string(secret.Data["github.token"]) == pacSecretNewData["github.token"] &&
					string(secret.Data["gitlab.token"]) == pacSecretNewData["gitlab.token"]
			}, timeout, interval).Should(BeTrue())
		})

		It("should not copy PaC secret into local namespace if GitHub application is used", func() {
			deleteSecret(namespacePaCSecretKey)

			setComponentDevfileModel(resourceKey)

			ensurePaCRepositoryCreated(resourceKey)

			ensureSecretNotCreated(namespacePaCSecretKey)
		})

		It("should reuse the same webhook secret for multicomponent repository", func() {
			github.CreatePaCPullRequest = func(c *github.GithubClient, d *github.PaCPullRequestData) (string, error) {
				return "", nil
			}

			var webhookSecretStrings []string
			github.SetupPaCWebhook = func(g *github.GithubClient, webhookUrl, webhookSecret, owner, repository string) error {
				webhookSecretStrings = append(webhookSecretStrings, webhookSecret)
				return nil
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			component1Key := resourceKey

			component2Key := types.NamespacedName{Name: "component2", Namespace: HASAppNamespace}
			component2 := &appstudiov1alpha1.Component{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      component2Key.Name,
					Namespace: component2Key.Namespace,
					Annotations: map[string]string{
						gitops.PaCAnnotation: "1",
					},
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName:  component2Key.Name,
					Application:    HASAppName,
					ContainerImage: "registry.io/username/image2:tag2",
					Source: appstudiov1alpha1.ComponentSource{
						ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
							GitSource: &appstudiov1alpha1.GitSource{
								URL: SampleRepoLink,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, component2)).Should(Succeed())
			defer deleteComponent(component2Key)

			setComponentDevfileModel(component1Key)
			setComponentDevfileModel(component2Key)

			ensureSecretCreated(namespacePaCSecretKey)
			ensureSecretCreated(webhookSecretKey)

			ensurePaCRepositoryCreated(resourceKey) // TODO one or two PaC repos?

			ensureComponentInitialBuildAnnotationState(component1Key, true)
			ensureComponentInitialBuildAnnotationState(component2Key, true)

			Expect(len(webhookSecretStrings) > 0).To(BeTrue())
			for _, webhookSecret := range webhookSecretStrings {
				Expect(webhookSecret).To(Equal(webhookSecretStrings[0]))
			}
		})

		It("should use different webhook secrets for different components of the same application", func() {
			github.CreatePaCPullRequest = func(c *github.GithubClient, d *github.PaCPullRequestData) (string, error) {
				return "", nil
			}

			var webhookSecretStrings []string
			github.SetupPaCWebhook = func(g *github.GithubClient, webhookUrl, webhookSecret, owner, repository string) error {
				webhookSecretStrings = append(webhookSecretStrings, webhookSecret)
				return nil
			}

			pacSecretData := map[string]string{"github.token": "ghp_token"}
			createSecret(pacSecretKey, pacSecretData)

			component1Key := resourceKey

			component2Key := types.NamespacedName{Name: "component2", Namespace: HASAppNamespace}
			component2 := &appstudiov1alpha1.Component{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      component2Key.Name,
					Namespace: component2Key.Namespace,
					Annotations: map[string]string{
						gitops.PaCAnnotation: "1",
					},
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName:  component2Key.Name,
					Application:    HASAppName,
					ContainerImage: "registry.io/username/image2:tag2",
					Source: appstudiov1alpha1.ComponentSource{
						ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
							GitSource: &appstudiov1alpha1.GitSource{
								URL: "https://github.com/devfile-samples/devfile-sample-go-basic",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, component2)).Should(Succeed())
			defer deleteComponent(component2Key)

			setComponentDevfileModel(component1Key)
			setComponentDevfileModel(component2Key)

			ensureSecretCreated(namespacePaCSecretKey)
			ensureSecretCreated(webhookSecretKey)

			ensurePaCRepositoryCreated(component1Key)
			ensurePaCRepositoryCreated(component2Key)

			ensureComponentInitialBuildAnnotationState(component1Key, true)
			ensureComponentInitialBuildAnnotationState(component2Key, true)

			Expect(len(webhookSecretStrings)).To(Equal(2))
			Expect(webhookSecretStrings[0]).ToNot(Equal(webhookSecretStrings[1]))
		})

		It("should not set initial build annotation if PaC definitions PR submition failed", func() {
			github.CreatePaCPullRequest = func(c *github.GithubClient, d *github.PaCPullRequestData) (string, error) {
				return "", errors.New("Failed to submit PaC definitions PR")
			}

			setComponentDevfileModel(resourceKey)

			ensurePaCRepositoryCreated(resourceKey)

			ensureComponentInitialBuildAnnotationState(resourceKey, false)
		})

		It("should not submit PaC definitions PR if PaC secret is missing", func() {
			github.CreatePaCPullRequest = func(c *github.GithubClient, d *github.PaCPullRequestData) (string, error) {
				Fail("PR creation should not be invoked")
				return "", nil
			}

			deleteSecret(pacSecretKey)

			setComponentDevfileModel(resourceKey)

			ensureComponentInitialBuildAnnotationState(resourceKey, false)
		})

		It("should do nothing if the component devfile model is not set", func() {
			github.CreatePaCPullRequest = func(c *github.GithubClient, d *github.PaCPullRequestData) (string, error) {
				Fail("PR creation should not be invoked")
				return "", nil
			}

			ensureComponentInitialBuildAnnotationState(resourceKey, false)
		})

		It("should do nothing if initial build annotation is already set", func() {
			github.CreatePaCPullRequest = func(c *github.GithubClient, d *github.PaCPullRequestData) (string, error) {
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
			github.CreatePaCPullRequest = func(c *github.GithubClient, d *github.PaCPullRequestData) (string, error) {
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
	})

	Context("Test initial build", func() {

		_ = BeforeEach(func() {
			createComponent(resourceKey)
		}, 30)

		_ = AfterEach(func() {
			deleteComponentInitialPipelineRuns(resourceKey)
			deleteComponent(resourceKey)
		}, 30)

		It("should submit initial build", func() {
			setComponentDevfileModel(resourceKey)

			ensureOneInitialPipelineRunCreated(resourceKey)
		})

		It("should not submit initial build if the component devfile model is not set", func() {
			ensureNoInitialPipelineRunsCreated(resourceKey)
		})

		It("should not submit initial build if initial build annotation exists on the component", func() {
			component := getComponent(resourceKey)
			component.Annotations = make(map[string]string)
			component.Annotations[InitialBuildAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).Should(Succeed())

			setComponentDevfileModel(resourceKey)

			ensureNoInitialPipelineRunsCreated(resourceKey)
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

			ensureNoInitialPipelineRunsCreated(resourceKey)
		})
		It("should submit initial build if a container image is set to default repo and starting with namespace tag", func() {
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
					ContainerImage: gitops.GetDefaultImageRepo() + ":" + HASAppNamespace + "-tag",
					Source: appstudiov1alpha1.ComponentSource{
						ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
							GitSource: &appstudiov1alpha1.GitSource{
								URL: SampleRepoLink,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())

			setComponentDevfileModel(resourceKey)

			ensureOneInitialPipelineRunCreated(resourceKey)
		})
		It("should not submit initial build if a container image is set to default repo not starting with namespace tag", func() {
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
					ContainerImage: gitops.GetDefaultImageRepo() + ":test-tag",
					Source: appstudiov1alpha1.ComponentSource{
						ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
							GitSource: &appstudiov1alpha1.GitSource{
								URL: SampleRepoLink,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())

			setComponentDevfileModel(resourceKey)

			ensureNoInitialPipelineRunsCreated(resourceKey)
		})
	})

	Context("Check if build objects are created", func() {

		var gitSecret *corev1.Secret

		BeforeEach(func() {
			// Pre-create git secret
			gitSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      GitSecretName,
					Namespace: HASAppNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, gitSecret)).Should(Succeed())
			// Create component that refers to the git secret
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
					Secret:         GitSecretName,
					ContainerImage: "docker.io/foo/customized:default-test-component",
					Source: appstudiov1alpha1.ComponentSource{
						ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
							GitSource: &appstudiov1alpha1.GitSource{
								URL: SampleRepoLink,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, component)).Should(Succeed())
		})
		AfterEach(func() {
			// Clean up
			deleteComponentInitialPipelineRuns(resourceKey)
			deleteComponent(resourceKey)
			Expect(k8sClient.Delete(ctx, gitSecret)).Should(Succeed())
		})

		It("should create build objects when secret missing", func() {

			setComponentDevfileModel(resourceKey)

			// Wait until all resources created
			ensureOneInitialPipelineRunCreated(resourceKey)

			// Check that git credentials secret is annotated
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: GitSecretName, Namespace: HASAppNamespace}, gitSecret)).Should(Succeed())
			tektonGitAnnotation := gitSecret.ObjectMeta.Annotations["tekton.dev/git-0"]
			Expect(tektonGitAnnotation).To(Equal("https://github.com"))

			// Check that the pipeline service account has been linked with the Github authentication credentials
			var pipelineSA corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "pipeline", Namespace: HASAppNamespace}, &pipelineSA)).Should(Succeed())

			secretFound := false
			for _, secret := range pipelineSA.Secrets {
				if secret.Name == GitSecretName {
					secretFound = true
					break
				}
			}
			Expect(secretFound).To(BeTrue())

			// Check the pipeline run and its resources
			pipelineRuns := listComponentInitialPipelineRuns(resourceKey)
			Expect(len(pipelineRuns.Items)).To(Equal(1))
			pipelineRun := pipelineRuns.Items[0]

			Expect(pipelineRun.Spec.Params).ToNot(BeEmpty())
			for _, p := range pipelineRun.Spec.Params {
				if p.Name == "output-image" {
					Expect(p.Value.StringVal).To(Equal("docker.io/foo/customized:default-test-component"))
				}
				if p.Name == "git-url" {
					Expect(p.Value.StringVal).To(Equal(SampleRepoLink))
				}
			}

			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(gitopsprepare.AppStudioFallbackBuildBundle))

			Expect(pipelineRun.Spec.Workspaces).To(Not(BeEmpty()))
			for _, w := range pipelineRun.Spec.Workspaces {
				Expect(w.Name).NotTo(Equal("registry-auth"))
				if w.Name == "workspace" {
					Expect(w.VolumeClaimTemplate).NotTo(
						Equal(nil), "PipelineRun should have its own volumeClaimTemplate.")
				}
			}

		})

		It("should create build objects when secrets exists", func() {

			// Setup registry secret in local namespace
			registrySecretKey := types.NamespacedName{Name: gitopsprepare.RegistrySecret, Namespace: HASAppNamespace}
			createSecret(registrySecretKey, nil)
			setComponentDevfileModel(resourceKey)

			// Wait until all resources created
			ensureOneInitialPipelineRunCreated(resourceKey)

			// Check that git credentials secret is annotated
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: GitSecretName, Namespace: HASAppNamespace}, gitSecret)).Should(Succeed())
			tektonGitAnnotation := gitSecret.ObjectMeta.Annotations["tekton.dev/git-0"]
			Expect(tektonGitAnnotation).To(Equal("https://github.com"))

			// Check that the pipeline service account has been linked with the Github authentication credentials
			var pipelineSA corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "pipeline", Namespace: HASAppNamespace}, &pipelineSA)).Should(Succeed())

			secretFound := false
			for _, secret := range pipelineSA.Secrets {
				if secret.Name == GitSecretName {
					secretFound = true
					break
				}
			}
			Expect(secretFound).To(BeTrue())

			// Check the pipeline run and its resources
			pipelineRuns := listComponentInitialPipelineRuns(resourceKey)
			Expect(len(pipelineRuns.Items)).To(Equal(1))
			pipelineRun := pipelineRuns.Items[0]

			Expect(pipelineRun.Spec.Params).ToNot(BeEmpty())
			for _, p := range pipelineRun.Spec.Params {
				if p.Name == "output-image" {
					Expect(p.Value.StringVal).To(Equal("docker.io/foo/customized:default-test-component"))
				}
				if p.Name == "git-url" {
					Expect(p.Value.StringVal).To(Equal(SampleRepoLink))
				}
			}

			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(gitopsprepare.AppStudioFallbackBuildBundle))

			Expect(pipelineRun.Spec.Workspaces).To(Not(BeEmpty()))
			for _, w := range pipelineRun.Spec.Workspaces {
				if w.Name == "registry-auth" {
					Expect(w.Secret.SecretName).To(Equal(gitopsprepare.RegistrySecret))
				}
				if w.Name == "workspace" {
					Expect(w.VolumeClaimTemplate).NotTo(
						Equal(nil), "PipelineRun should have its own volumeClaimTemplate.")
				}
			}
		})
	})

	Context("Resolve the correct build bundle during the component's creation", func() {
		It("should use the build bundle specified if a configmap is set in the current namespace", func() {
			buildBundle := "quay.io/some-repo/some-bundle:0.0.1"

			componentKey := types.NamespacedName{Name: HASCompName, Namespace: HASAppNamespace}
			configMapKey := types.NamespacedName{Name: gitopsprepare.BuildBundleConfigMapName, Namespace: HASAppNamespace}

			createConfigMap(configMapKey, map[string]string{
				gitopsprepare.BuildBundleConfigMapKey: buildBundle,
			})
			createComponent(componentKey)
			setComponentDevfileModel(componentKey)

			ensureOneInitialPipelineRunCreated(componentKey)
			pipelineRuns := listComponentInitialPipelineRuns(componentKey)

			Expect(pipelineRuns.Items[0].Spec.PipelineRef.Bundle).To(Equal(buildBundle))

			deleteComponent(componentKey)
			deleteComponentInitialPipelineRuns(componentKey)
			deleteConfigMap(configMapKey)
		})

		It("should use the build bundle specified if a configmap is set in the default bundle namespace", func() {
			buildBundle := "quay.io/some-repo/some-bundle:0.0.2"

			componentKey := types.NamespacedName{Name: HASCompName, Namespace: HASAppNamespace}
			configMapKey := types.NamespacedName{Name: gitopsprepare.BuildBundleConfigMapName, Namespace: gitopsprepare.BuildBundleDefaultNamespace}

			createNamespace(gitopsprepare.BuildBundleDefaultNamespace)
			createConfigMap(configMapKey, map[string]string{
				gitopsprepare.BuildBundleConfigMapKey: buildBundle,
			})
			createComponent(componentKey)
			setComponentDevfileModel(componentKey)

			ensureOneInitialPipelineRunCreated(componentKey)
			pipelineRuns := listComponentInitialPipelineRuns(componentKey)

			Expect(pipelineRuns.Items[0].Spec.PipelineRef.Bundle).To(Equal(buildBundle))

			deleteComponent(componentKey)
			deleteComponentInitialPipelineRuns(componentKey)
			deleteConfigMap(configMapKey)
		})

		It("should use the fallback build bundle in case no configmap is found", func() {
			componentKey := types.NamespacedName{Name: HASCompName, Namespace: HASAppNamespace}
			createComponent(componentKey)
			setComponentDevfileModel(componentKey)

			ensureOneInitialPipelineRunCreated(componentKey)
			pipelineRuns := listComponentInitialPipelineRuns(componentKey)

			Expect(pipelineRuns.Items[0].Spec.PipelineRef.Bundle).To(Equal(gitopsprepare.AppStudioFallbackBuildBundle))

			deleteComponent(componentKey)
			deleteComponentInitialPipelineRuns(componentKey)
		})
	})
})
