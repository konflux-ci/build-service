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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
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
			Name:      componentLookupKey.Name,
			Namespace: componentLookupKey.Namespace,
		},
		Spec: appstudiov1alpha1.ComponentSpec{
			ComponentName: componentLookupKey.Name,
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

func ensureNoPipelineRunsCreated(componentLookupKey types.NamespacedName) {
	count := 0
	Eventually(func() bool {
		if count < 10 {
			Expect(len(listComponentPipelienRuns(componentLookupKey).Items)).Should(Equal(0))
			count++
			return false
		}
		return true
	}, timeout, interval).Should(BeTrue())
}

var _ = Describe("Component build controller", func() {

	Context("Test initial build", func() {
		var (
			// All related to the component resources have the same key (but different type)
			resourceKey = types.NamespacedName{Name: HASCompName, Namespace: HASAppNamespace}
		)

		_ = BeforeEach(func() {
			createComponent(resourceKey)
		}, 30)

		_ = AfterEach(func() {
			deleteComponentPipelienRuns(resourceKey)
			deleteComponent(resourceKey)
		}, 30)

		It("should submit initial build", func() {
			setComponentDevfileModel(resourceKey)

			component := getComponent(resourceKey)
			Eventually(func() bool {
				pipelineRuns := listComponentPipelienRuns(resourceKey).Items
				if len(pipelineRuns) != 1 {
					return false
				}
				pipelineRun := pipelineRuns[0]
				return isOwnedBy(pipelineRun.OwnerReferences, *component)
			}, timeout, interval).Should(BeTrue())
		})

		It("should not submit intial build if the component devfile model is not set", func() {
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
	})

})
