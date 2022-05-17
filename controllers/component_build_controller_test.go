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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops/prepare"
	//+kubebuilder:scaffold:imports
)

const (
	timeout  = time.Second * 15
	interval = time.Millisecond * 250
)

const (
	HASAppName      = "test-application"
	HASCompName     = "test-component"
	HASAppNamespace = "default"
	SampleRepoLink  = "https://github.com/devfile-samples/devfile-sample-java-springboot-basic"
	GitSecretName   = "git-secret"
)

func isOwnedBy(resource []metav1.OwnerReference, component appstudiov1alpha1.Component) bool {
	if len(resource) == 0 {
		return false
	}
	if resource[0].Kind == "Component" &&
		resource[0].APIVersion == "appstudio.redhat.com/v1alpha1" &&
		resource[0].Name == component.Name {
		return true
	}
	return false
}

// createComponent creates sample component resource and verifies it was properly created
func createComponent(componentLookupKey types.NamespacedName) {
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
			ComponentName: HASCompName,
			Application:   HASAppName,
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

	getComponent(componentLookupKey)
}

func createConfigMap(name string, namespace string, data map[string]string) {
	configMap := corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		Data: data,
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Expect(k8sClient.Create(ctx, &configMap)).Should(Succeed())
}

func getComponent(componentLookupKey types.NamespacedName) *appstudiov1alpha1.Component {
	component := &appstudiov1alpha1.Component{}
	Eventually(func() bool {
		k8sClient.Get(ctx, componentLookupKey, component)
		return component.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return component
}

func setComponentDevfileModel(componentLookupKey types.NamespacedName) {
	component := &appstudiov1alpha1.Component{}
	Eventually(func() error {
		k8sClient.Get(ctx, componentLookupKey, component)
		component.Status.Devfile = "version: 2.2.0"
		return k8sClient.Status().Update(ctx, component)
	}, timeout, interval).Should(Succeed())

	component = getComponent(componentLookupKey)
	Expect(component.Status.Devfile).Should(Not(Equal("")))
}

// deleteComponent deletes the specified component resource and verifies it was properly deleted
func deleteComponent(componentLookupKey types.NamespacedName) {
	// Delete
	Eventually(func() error {
		f := &appstudiov1alpha1.Component{}
		k8sClient.Get(ctx, componentLookupKey, f)
		return k8sClient.Delete(ctx, f)
	}, timeout, interval).Should(Succeed())

	// Wait for delete to finish
	Eventually(func() error {
		f := &appstudiov1alpha1.Component{}
		return k8sClient.Get(ctx, componentLookupKey, f)
	}, timeout, interval).ShouldNot(Succeed())
}

func listComponentPipelienRuns(componentLookupKey types.NamespacedName) *tektonapi.PipelineRunList {
	pipelineRuns := &tektonapi.PipelineRunList{}
	labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
		LabelSelector: "build.appstudio.openshift.io/component=" + componentLookupKey.Name,
	}}
	err := k8sClient.List(ctx, pipelineRuns, &labelSelectors)
	Expect(err).ToNot(HaveOccurred())
	return pipelineRuns
}

func deleteComponentPipelienRuns(componentLookupKey types.NamespacedName) {
	for _, pipelineRun := range listComponentPipelienRuns(componentLookupKey).Items {
		Expect(k8sClient.Delete(ctx, &pipelineRun)).Should(Succeed())
	}
}

func ensureOnePipelineRunCreated(componentLookupKey types.NamespacedName) {
	component := getComponent(componentLookupKey)
	Eventually(func() bool {
		pipelineRuns := listComponentPipelienRuns(componentLookupKey).Items
		if len(pipelineRuns) != 1 {
			return false
		}
		pipelineRun := pipelineRuns[0]
		return isOwnedBy(pipelineRun.OwnerReferences, *component)
	}, timeout, interval).Should(BeTrue())
}

func ensureNoPipelineRunsCreated(componentLookupKey types.NamespacedName) {
	component := getComponent(componentLookupKey)

	pipelineRuns := &tektonapi.PipelineRunList{}
	Consistently(func() bool {
		labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
			LabelSelector: "build.appstudio.openshift.io/component=" + component.Name,
		}}
		k8sClient.List(ctx, pipelineRuns, &labelSelectors)
		return len(pipelineRuns.Items) == 0
	}, timeout, interval).WithTimeout(10 * time.Second).Should(BeTrue())
}

var _ = Describe("Component initial build controller", func() {

	var (
		// All related to the component resources have the same key (but different type)
		resourceKey = types.NamespacedName{Name: HASCompName, Namespace: HASAppNamespace}
	)

	Context("Test initial build", func() {

		_ = BeforeEach(func() {
			createComponent(resourceKey)
		}, 30)

		_ = AfterEach(func() {
			deleteComponentPipelienRuns(resourceKey)
			deleteComponent(resourceKey)
		}, 30)

		It("should submit initial build", func() {
			setComponentDevfileModel(resourceKey)

			ensureOnePipelineRunCreated(resourceKey)
		})

		It("should not submit initial build if the component devfile model is not set", func() {
			ensureNoPipelineRunsCreated(resourceKey)
		})

		It("should not submit initial build if initial build annotation exists on the component", func() {
			component := getComponent(resourceKey)
			component.Annotations = make(map[string]string)
			component.Annotations[InitialBuildAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, component)).Should(Succeed())

			setComponentDevfileModel(resourceKey)

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

	Context("Check if build objects are created", func() {

		It("should create build objects", func() {
			// Pre-create git secret
			gitSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      GitSecretName,
					Namespace: HASAppNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, gitSecret)).Should(Succeed())

			// Configure the build bundle
			buildBundle := "quay.io/some-repo/some-bundle:0.0.1"
			createConfigMap(prepare.BuildBundleConfigMapName, HASAppNamespace, map[string]string{
				prepare.BuildBundleConfigMapKey: buildBundle,
			})

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

			setComponentDevfileModel(resourceKey)

			// Wait until all resources created
			ensureOnePipelineRunCreated(resourceKey)

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
			pipelineRuns := listComponentPipelienRuns(resourceKey)
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

			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(buildBundle))

			Expect(pipelineRun.Spec.Workspaces).To(Not(BeEmpty()))
			for _, w := range pipelineRun.Spec.Workspaces {
				if w.Name == "registry-auth" {
					Expect(w.Secret.SecretName).To(Equal("redhat-appstudio-registry-pull-secret"))
				}
				if w.Name == "workspace" {
					Expect(w.PersistentVolumeClaim.ClaimName).To(Equal("appstudio"))
					Expect(w.SubPath).To(ContainSubstring("/initialbuild-"))
				}
			}

			// Clean up
			deleteComponentPipelienRuns(resourceKey)
			deleteComponent(resourceKey)
		})
	})
})
