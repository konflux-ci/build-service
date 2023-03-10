/*
Copyright 2023 Red Hat, Inc.

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Component PipelineRuns pruner controller", func() {

	var (
		// All related to the component resources have the same key (but different type)
		resourceKey = types.NamespacedName{Name: HASCompName, Namespace: HASAppNamespace}
	)

	Context("Test Component PipelineRuns pruning ", func() {

		_ = BeforeEach(func() {
			createComponentForPaCBuild(getSampleComponentData(resourceKey))
		})

		It("should not fail if nothing to prune", func() {
			Expect(len(listComponentPipelineRuns(resourceKey))).To(Equal(0))

			deleteComponent(resourceKey)
			waitNoPipelineRunsForComponent(resourceKey)
		})

		It("should prune single PipelineRun", func() {
			createPaCPipelineRunWithName(resourceKey, resourceKey.Name)
			Expect(len(listComponentPipelineRuns(resourceKey))).To(Equal(1))

			deleteComponent(resourceKey)
			waitNoPipelineRunsForComponent(resourceKey)
		})

		It("should prune all PipelineRuns", func() {
			createPaCPipelineRunWithName(resourceKey, "component-on-pull-request-j7gpx")
			createPaCPipelineRunWithName(resourceKey, "component-on-pull-request-j05ew")
			createPaCPipelineRunWithName(resourceKey, "component-on-push-vk1t5")
			createPaCPipelineRunWithName(resourceKey, "component-on-push-b9i8p")
			Expect(len(listComponentPipelineRuns(resourceKey))).To(Equal(4))

			deleteComponent(resourceKey)
			waitNoPipelineRunsForComponent(resourceKey)
		})

		It("should prune only PipelineRuns that belong to the Component", func() {
			createPaCPipelineRunWithName(resourceKey, "component-on-pull-request-j7gpx")
			createPaCPipelineRunWithName(resourceKey, "component-on-push-vk1t5")
			Expect(len(listComponentPipelineRuns(resourceKey))).To(Equal(2))

			anotherComponentKey := types.NamespacedName{Namespace: HASAppNamespace, Name: "component2"}
			Expect(k8sClient.Create(ctx, getSampleComponentData(anotherComponentKey))).Should(Succeed())
			getComponent(anotherComponentKey)

			anotherComponentPipelineRun1Name := "component2-on-pull-request-5r2je"
			anotherComponentPipelineRun2Name := "component2-on-push-owt1l"
			createPaCPipelineRunWithName(anotherComponentKey, anotherComponentPipelineRun1Name)
			createPaCPipelineRunWithName(anotherComponentKey, anotherComponentPipelineRun2Name)
			Expect(len(listComponentPipelineRuns(anotherComponentKey))).To(Equal(2))

			deleteComponent(resourceKey)
			waitNoPipelineRunsForComponent(resourceKey)

			anotherComponentPipelineRuns := listComponentPipelineRuns(anotherComponentKey)
			Expect(len(anotherComponentPipelineRuns)).To(Equal(2))
			for _, pipelineRun := range anotherComponentPipelineRuns {
				switch pipelineRun.Name {
				case anotherComponentPipelineRun1Name, anotherComponentPipelineRun2Name:
					continue
				default:
					defer GinkgoRecover()
					Fail(fmt.Sprintf("Found unexpected %s PipelineRun for the %s Component", pipelineRun.Name, anotherComponentKey.Name))
				}
			}

			deleteComponent(anotherComponentKey)
			waitNoPipelineRunsForComponent(anotherComponentKey)
		})

		It("should not prune PipelineRuns that belong to the Component with the same name in a different namespace", func() {
			createPaCPipelineRunWithName(resourceKey, "component-on-pull-request-z2opy")
			createPaCPipelineRunWithName(resourceKey, "component-on-push-pk8u5")
			Expect(len(listComponentPipelineRuns(resourceKey))).To(Equal(2))

			anotherComponentNamespace := "test-namespace"
			createNamespace(anotherComponentNamespace)

			anotherComponentKey := types.NamespacedName{Namespace: anotherComponentNamespace, Name: resourceKey.Name}
			Expect(k8sClient.Create(ctx, getSampleComponentData(anotherComponentKey))).Should(Succeed())
			getComponent(anotherComponentKey)

			anotherComponentPipelineRun1Name := "component2-on-pull-request-ia8c4"
			anotherComponentPipelineRun2Name := "component2-on-push-l0dni"
			createPaCPipelineRunWithName(anotherComponentKey, anotherComponentPipelineRun1Name)
			createPaCPipelineRunWithName(anotherComponentKey, anotherComponentPipelineRun2Name)
			Expect(len(listComponentPipelineRuns(anotherComponentKey))).To(Equal(2))

			deleteComponent(resourceKey)
			waitNoPipelineRunsForComponent(resourceKey)

			anotherComponentPipelineRuns := listComponentPipelineRuns(anotherComponentKey)
			Expect(len(anotherComponentPipelineRuns)).To(Equal(2))
			for _, pipelineRun := range anotherComponentPipelineRuns {
				switch pipelineRun.Name {
				case anotherComponentPipelineRun1Name, anotherComponentPipelineRun2Name:
					continue
				default:
					defer GinkgoRecover()
					Fail(fmt.Sprintf("Found unexpected %s PipelineRun for the %s Component", pipelineRun.Name, anotherComponentKey.Name))
				}
			}

			deleteComponent(anotherComponentKey)
			waitNoPipelineRunsForComponent(anotherComponentKey)
			deleteNamespace(anotherComponentNamespace)
		})
	})

})
