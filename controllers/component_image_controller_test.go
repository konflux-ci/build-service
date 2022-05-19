/*
Copyright 2022 Red Hat, Inc.

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
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	//+kubebuilder:scaffold:imports
)

var _ = Describe("New Component Image Controller", func() {

	var (
		// All related to the component resources have the same key (but different type)
		resourceKey = types.NamespacedName{Name: HASCompName, Namespace: HASAppNamespace}
	)

	Context("Test component image update", func() {

		_ = BeforeEach(func() {
			createComponent(resourceKey)
			setComponentDevfileModel(resourceKey)
		}, 30)

		_ = AfterEach(func() {
			deleteComponent(resourceKey)
			deleteAllTaskRuns()
			deleteAllPipelineRuns()
		}, 30)

		It("Should not update component image after initial build if image reference is the same", func() {
			ensureOneInitialPipelineRunCreated(resourceKey)
			initialBuildPipelineKey := types.NamespacedName{
				Name:      listComponentInitialPipelineRuns(resourceKey).Items[0].Name,
				Namespace: HASAppNamespace,
			}
			createBuildTaskRunWithImage(initialBuildPipelineKey, ComponentContainerImage)
			succeedInitialPipelineRun(resourceKey)

			component := getComponent(resourceKey)
			Expect(component.Spec.ContainerImage).To(Equal(ComponentContainerImage))
		})

		It("Should update component image after initial build if the image was changed", func() {
			newImage := ComponentContainerImage + "-initial1234"

			ensureOneInitialPipelineRunCreated(resourceKey)
			initialBuildPipelineKey := types.NamespacedName{
				Name:      listComponentInitialPipelineRuns(resourceKey).Items[0].Name,
				Namespace: HASAppNamespace,
			}
			createBuildTaskRunWithImage(initialBuildPipelineKey, newImage)
			succeedInitialPipelineRun(resourceKey)

			Eventually(func() bool {
				component := getComponent(resourceKey)
				return component.Spec.ContainerImage == newImage
			}, timeout, interval).Should(BeTrue())
		})

		It("Should update component image after webhook build", func() {
			newImage := ComponentContainerImage + "-commit1234"

			ensureOneInitialPipelineRunCreated(resourceKey)
			initialBuildPipelineKey := types.NamespacedName{
				Name:      listComponentInitialPipelineRuns(resourceKey).Items[0].Name,
				Namespace: HASAppNamespace,
			}
			createBuildTaskRunWithImage(initialBuildPipelineKey, ComponentContainerImage+"-initial")
			succeedInitialPipelineRun(resourceKey)

			createWebhookPipelineRun(resourceKey)
			createBuildTaskRunWithImage(resourceKey, newImage)
			succeedWebhookPipelineRun(resourceKey)

			Eventually(func() bool {
				component := getComponent(resourceKey)
				return component.Spec.ContainerImage == newImage
			}, timeout, interval).Should(BeTrue())
		})

		It("Should not update component image after webhook build if new image build was skipped", func() {
			ensureOneInitialPipelineRunCreated(resourceKey)
			succeedInitialPipelineRun(resourceKey)

			createWebhookPipelineRun(resourceKey)
			succeedWebhookPipelineRun(resourceKey)

			component := getComponent(resourceKey)
			Expect(component.Spec.ContainerImage).To(Equal(ComponentContainerImage))
		})

		It("Should not update component image after failed build", func() {
			ensureOneInitialPipelineRunCreated(resourceKey)
			succeedInitialPipelineRun(resourceKey)

			createWebhookPipelineRun(resourceKey)
			createBuildTaskRunWithImage(resourceKey, ComponentContainerImage+"-new")
			failWebhookPipelineRun(resourceKey)

			component := getComponent(resourceKey)
			Expect(component.Spec.ContainerImage).To(Equal(ComponentContainerImage))
		})

		It("Should not do anything if PipelineRun doesn't belong to any component", func() {
			ensureOneInitialPipelineRunCreated(resourceKey)
			succeedInitialPipelineRun(resourceKey)

			createWebhookPipelineRun(resourceKey)
			// Delete relation to the Component
			pipelineRun := &tektonapi.PipelineRun{}
			Expect(k8sClient.Get(ctx, resourceKey, pipelineRun)).Should(Succeed())
			if pipelineRun.Annotations != nil {
				delete(pipelineRun.Annotations, ComponentNameLabelName)
			}
			if pipelineRun.Labels != nil {
				delete(pipelineRun.Labels, ComponentNameLabelName)
			}
			Expect(k8sClient.Update(ctx, pipelineRun)).Should(Succeed())

			createBuildTaskRunWithImage(resourceKey, ComponentContainerImage+"-new")
			succeedWebhookPipelineRun(resourceKey)

			component := getComponent(resourceKey)
			Expect(component.Spec.ContainerImage).To(Equal(ComponentContainerImage))
		})
	})
})
