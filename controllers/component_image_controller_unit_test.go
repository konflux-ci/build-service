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

	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetImageReferenceFromBuildPipeline(t *testing.T) {
	getPipelineWithResults := func(image, digest string) tektonapi.PipelineRun {
		var pipelineResults []tektonapi.PipelineRunResult
		if image != "" {
			pipelineResults = append(pipelineResults, tektonapi.PipelineRunResult{
				Name:  "IMAGE_URL",
				Value: image,
			})
		}
		if digest != "" {
			pipelineResults = append(pipelineResults, tektonapi.PipelineRunResult{
				Name:  "IMAGE_DIGEST",
				Value: digest,
			})
		}

		return tektonapi.PipelineRun{
			Status: tektonapi.PipelineRunStatus{
				PipelineRunStatusFields: tektonapi.PipelineRunStatusFields{
					PipelineResults: pipelineResults,
				},
			},
		}
	}

	tests := []struct {
		name                   string
		buildPipelineRun       tektonapi.PipelineRun
		expectedImageReference string
		wantErr                bool
	}{
		{
			name:                   "should use image and tag if no digest set",
			buildPipelineRun:       getPipelineWithResults("registry.io/image:tag", ""),
			expectedImageReference: "registry.io/image:tag",
			wantErr:                false,
		},
		{
			name:                   "should use image and digest",
			buildPipelineRun:       getPipelineWithResults("registry.io/image", "sha256:abcd12345"),
			expectedImageReference: "registry.io/image@sha256:abcd12345",
			wantErr:                false,
		},
		{
			name:                   "should use image and digest ignoring image tag",
			buildPipelineRun:       getPipelineWithResults("registry.io/image:tag", "sha256:abcd12345"),
			expectedImageReference: "registry.io/image@sha256:abcd12345",
			wantErr:                false,
		},
		{
			name:                   "should fail if both image and digest are not set",
			buildPipelineRun:       getPipelineWithResults("", ""),
			expectedImageReference: "",
			wantErr:                true,
		},
		{
			name:                   "should fail if image is not set",
			buildPipelineRun:       getPipelineWithResults("", "sha256:abcd12345"),
			expectedImageReference: "",
			wantErr:                true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imageReference, err := getImageReferenceFromBuildPipeline(tt.buildPipelineRun)

			if tt.wantErr {
				if err == nil {
					t.Errorf("extraction of image reference should fail")
				}
			} else {
				if imageReference != tt.expectedImageReference {
					t.Errorf("expected %s image reference, but got %s", tt.expectedImageReference, imageReference)
				}
			}
		})
	}
}

func TestGenerateApplicationSnapshot(t *testing.T) {
	application := &appstudiov1alpha1.Application{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "appstudio.redhat.com/v1alpha1",
			Kind:       "Application",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      HASAppName,
			Namespace: HASAppNamespace,
		},
	}

	getComponent := func(componentName, image string) appstudiov1alpha1.Component {
		return appstudiov1alpha1.Component{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "appstudio.redhat.com/v1alpha1",
				Kind:       "Component",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      componentName,
				Namespace: HASAppNamespace,
			},
			Spec: appstudiov1alpha1.ComponentSpec{
				ComponentName:  componentName,
				Application:    HASAppName,
				ContainerImage: image,
			},
		}
	}

	component1Name := HASCompName + "-1"
	image1 := "registry.io/username1/image-name:tag"
	component1 := getComponent(component1Name, image1)

	component2Name := HASCompName + "-2"
	image2 := "registry.io/username2/image-name:tag"
	component2 := getComponent(component2Name, image2)

	tests := []struct {
		name        string
		application *appstudiov1alpha1.Application
		components  []appstudiov1alpha1.Component
		wantErr     bool
	}{
		{
			name:        "should generate application snapshot for application with one component",
			application: application,
			components: []appstudiov1alpha1.Component{
				component1,
			},
			wantErr: false,
		},
		{
			name:        "should generate application snapshot for application with two components",
			application: application,
			components: []appstudiov1alpha1.Component{
				component1,
				component2,
			},
			wantErr: false,
		},
		{
			name:        "should fail to generate application snapshot for application without components",
			application: application,
			components:  []appstudiov1alpha1.Component{},
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applicationSnapshot, err := generateApplicationSnapshot(tt.application, tt.components)

			if tt.wantErr {
				if err == nil {
					t.Errorf("application snapshot generation must fail")
				}
				return
			}

			if err != nil {
				t.Errorf("application snapshot must be generated without error")
			}

			if applicationSnapshot.GetName() == "" {
				t.Errorf("application snapshot name must not be empty")
			}
			if applicationSnapshot.GetNamespace() == "" {
				t.Errorf("application snapshot namespace must not be empty")
			}

			if len(applicationSnapshot.GetLabels()) == 0 {
				t.Errorf("application snapshot must have application name label")
			}
			if applicationSnapshot.GetLabels()[ApplicationNameLabelName] != tt.application.GetName() {
				t.Errorf("application snapshot must have application name label")
			}

			if len(applicationSnapshot.GetOwnerReferences()) != 1 {
				t.Errorf("application snapshot must be owned by application")
			}
			owner := applicationSnapshot.GetOwnerReferences()[0]
			if owner.Name != tt.application.GetName() || owner.Kind != "Application" {
				t.Errorf("application snapshot must be owned by application")
			}

			if applicationSnapshot.Spec.DisplayName == "" {
				t.Errorf("application snapshot display name must not be empty")
			}
			if applicationSnapshot.Spec.DisplayDescription == "" {
				t.Errorf("application snapshot display description must not be empty")
			}

			if len(applicationSnapshot.Spec.Components) != len(tt.components) {
				t.Errorf("application snapshot must contain all application components")
			}
			for _, component := range tt.components {
				componentFoundInSnapshot := false
				for _, applicationSnapshotComponent := range applicationSnapshot.Spec.Components {
					if applicationSnapshotComponent.Name == component.Name {
						if applicationSnapshotComponent.ContainerImage != component.Spec.ContainerImage {
							t.Errorf("application snapshot component must reflect application component image")
						}

						componentFoundInSnapshot = true
						break
					}
				}
				if !componentFoundInSnapshot {
					t.Errorf("application snapshot must contain all application components")
				}
			}
		})
	}
}

func TestGetApplicationSnapshotName(t *testing.T) {
	t.Run("should generate application snapshot name", func(t *testing.T) {
		applicationName := "test-application"

		applicationSnapshotName := getApplicationSnapshotName(applicationName)

		if len(applicationSnapshotName) > k8sNameMaxLen {
			t.Errorf("application snapshot name exceeds max allowed length")
		}
		if !strings.Contains(applicationSnapshotName, applicationName) {
			t.Errorf("application name must be part of application snapshot name")
		}
	})

	t.Run("should generate different application snapshot name for the same component", func(t *testing.T) {
		applicationName := "test-application"

		applicationSnapshotName1 := getApplicationSnapshotName(applicationName)
		applicationSnapshotName2 := getApplicationSnapshotName(applicationName)

		if len(applicationSnapshotName1) > k8sNameMaxLen {
			t.Errorf("application snapshot name exceeds max allowed length")
		}
		if len(applicationSnapshotName2) > k8sNameMaxLen {
			t.Errorf("application snapshot name exceeds max allowed length")
		}
		if applicationSnapshotName1 == applicationSnapshotName2 {
			t.Errorf("application snapshot name must be unique")
		}
	})

	t.Run("should generate application snapshot name if application name is too big", func(t *testing.T) {
		applicationName := "a"
		for i := 0; i < 25; i++ {
			applicationName += "1234567890"
		}

		applicationSnapshotName := getApplicationSnapshotName(applicationName)

		if len(applicationSnapshotName) > k8sNameMaxLen {
			t.Errorf("application snapshot name exceeds max allowed length")
		}
	})
}
