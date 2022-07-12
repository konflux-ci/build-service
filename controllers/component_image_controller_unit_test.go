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
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateApplicationSnapshot(t *testing.T) {
	g := NewGomegaWithT(t)

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
			ContainerImage: ComponentContainerImage,
			Source: appstudiov1alpha1.ComponentSource{
				ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
					GitSource: &appstudiov1alpha1.GitSource{
						URL: SampleRepoLink,
					},
				},
			},
		},
	}

	t.Run("should generate application snapshot", func(t *testing.T) {
		image := "registry.io/username/image-name:tag"

		applicationSnapshot := generateApplicationSnapshot(component, image)

		g.Expect(applicationSnapshot.GetName()).ToNot(BeEmpty())
		g.Expect(applicationSnapshot.GetNamespace()).To(Equal(component.GetNamespace()))
		g.Expect(len(applicationSnapshot.GetLabels())).To(BeNumerically(">", 0))
		g.Expect(applicationSnapshot.GetLabels()[ComponentNameLabelName]).To(Equal(component.GetName()))
		g.Expect(applicationSnapshot.Spec.Application).To(Equal(component.Spec.Application))
		g.Expect(applicationSnapshot.Spec.DisplayName).ToNot(BeEmpty())
		g.Expect(applicationSnapshot.Spec.DisplayDescription).ToNot(BeEmpty())
		g.Expect(len(applicationSnapshot.Spec.Components)).To(BeNumerically("==", 1))
		g.Expect(applicationSnapshot.Spec.Components[0].Name).To(Equal(component.GetName()))
		g.Expect(applicationSnapshot.Spec.Components[0].ContainerImage).To(Equal(image))
	})
}

func TestGetApplicationSnapshotName(t *testing.T) {
	g := NewGomegaWithT(t)

	t.Run("should generate application snapshot name", func(t *testing.T) {
		componentName := "test-component"

		applicationSnapshotName := getApplicationSnapshotName(componentName)

		g.Expect(len(applicationSnapshotName)).To(BeNumerically("<=", k8sNameMaxLen))
		g.Expect(strings.Contains(applicationSnapshotName, componentName)).To(BeTrue())
	})

	t.Run("should generate different application snapshot name for the same component", func(t *testing.T) {
		componentName := "test-component"

		applicationSnapshotName1 := getApplicationSnapshotName(componentName)
		applicationSnapshotName2 := getApplicationSnapshotName(componentName)

		g.Expect(len(applicationSnapshotName1)).To(BeNumerically("<=", k8sNameMaxLen))
		g.Expect(len(applicationSnapshotName2)).To(BeNumerically("<=", k8sNameMaxLen))
		g.Expect(applicationSnapshotName1).ToNot(Equal(applicationSnapshotName2))
	})

	t.Run("should generate application snapshot name if component name is too big", func(t *testing.T) {
		componentName := "c"
		for i := 0; i < 25; i++ {
			componentName += "1234567890"
		}

		applicationSnapshotName := getApplicationSnapshotName(componentName)

		g.Expect(len(applicationSnapshotName)).To(BeNumerically("<=", k8sNameMaxLen))
	})
}
