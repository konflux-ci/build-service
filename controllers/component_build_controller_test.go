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
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	routev1 "github.com/openshift/api/route/v1"
	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	gitopsprepare "github.com/redhat-appstudio/build-service/pkg/gitops/prepare"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	triggersapi "github.com/tektoncd/triggers/pkg/apis/triggers/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	//+kubebuilder:scaffold:imports
)

const (
	timeout  = time.Second * 10
	duration = time.Second * 10
	interval = time.Millisecond * 250
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

// Simple function to create, retrieve from k8s, and return a simple Application CR
func createAndFetchSimpleApp(name string, namespace string, display string, description string) *appstudiov1alpha1.Application {
	hasApp := &appstudiov1alpha1.Application{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "appstudio.redhat.com/v1alpha1",
			Kind:       "Application",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appstudiov1alpha1.ApplicationSpec{
			DisplayName: display,
			Description: description,
		},
	}

	Expect(k8sClient.Create(ctx, hasApp)).Should(Succeed())

	// Look up the has app resource that was created.
	// num(conditions) may still be < 1 on the first try, so retry until at least _some_ condition is set
	hasAppLookupKey := types.NamespacedName{Name: name, Namespace: namespace}
	fetchedHasApp := &appstudiov1alpha1.Application{}
	Eventually(func() bool {
		k8sClient.Get(ctx, hasAppLookupKey, fetchedHasApp)
		return len(fetchedHasApp.Status.Conditions) > 0
	}, timeout, interval).Should(BeTrue())

	return fetchedHasApp
}

func createAndFetchConfigMap(name string, namespace string, data map[string]string) *corev1.ConfigMap {
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

	configMapLookupKey := types.NamespacedName{Name: name, Namespace: namespace}
	createdConfigMap := &corev1.ConfigMap{}

	Eventually(func() bool {
		k8sClient.Get(context.Background(), configMapLookupKey, createdConfigMap)
		return reflect.DeepEqual(createdConfigMap.Data, data)
	}, timeout, interval).Should(BeTrue())

	return createdConfigMap
}

func createAndFetchComponent(name string, namespace string, application string, componentName string, gitUrl string) *appstudiov1alpha1.Component {
	hasComp := &appstudiov1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "appstudio.redhat.com/v1alpha1",
			Kind:       "Component",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appstudiov1alpha1.ComponentSpec{
			ComponentName: componentName,
			Application:   application,
			Source: appstudiov1alpha1.ComponentSource{
				ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
					GitSource: &appstudiov1alpha1.GitSource{
						URL: gitUrl,
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, hasComp)).Should(Succeed())

	hasCompLookupKey := types.NamespacedName{Name: name, Namespace: namespace}
	createdHasComp := &appstudiov1alpha1.Component{}
	Eventually(func() bool {
		k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)
		return len(createdHasComp.Status.Conditions) > 0 && createdHasComp.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())

	return createdHasComp
}

func deleteConfigMap(configMapLookupKey types.NamespacedName) {
	// Delete
	Eventually(func() error {
		f := &corev1.ConfigMap{}
		k8sClient.Get(context.Background(), configMapLookupKey, f)
		return k8sClient.Delete(context.Background(), f)
	}, timeout, interval).Should(Succeed())

	// Wait for delete to finish
	Eventually(func() error {
		f := &corev1.ConfigMap{}
		return k8sClient.Get(context.Background(), configMapLookupKey, f)
	}, timeout, interval).ShouldNot(Succeed())
}

// deleteHASAppCR deletes the specified hasApp resource and verifies it was properly deleted
func deleteHASAppCR(hasAppLookupKey types.NamespacedName) {
	// Delete
	Eventually(func() error {
		f := &appstudiov1alpha1.Application{}
		k8sClient.Get(ctx, hasAppLookupKey, f)
		return k8sClient.Delete(ctx, f)
	}, timeout, interval).Should(Succeed())

	// Wait for delete to finish
	Eventually(func() error {
		f := &appstudiov1alpha1.Application{}
		return k8sClient.Get(ctx, hasAppLookupKey, f)
	}, timeout, interval).ShouldNot(Succeed())
}

// deleteHASCompCR deletes the specified hasComp resource and verifies it was properly deleted
func deleteHASCompCR(hasCompLookupKey types.NamespacedName) {
	// Delete
	Eventually(func() error {
		f := &appstudiov1alpha1.Component{}
		k8sClient.Get(ctx, hasCompLookupKey, f)
		return k8sClient.Delete(ctx, f)
	}, timeout, interval).Should(Succeed())

	// Wait for delete to finish
	Eventually(func() error {
		f := &appstudiov1alpha1.Component{}
		return k8sClient.Get(ctx, hasCompLookupKey, f)
	}, timeout, interval).ShouldNot(Succeed())
}

func fetchPipelineRun(componentName string) tektonapi.PipelineRun {
	pipelineRuns := tektonapi.PipelineRunList{}

	Eventually(func() bool {
		labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
			LabelSelector: "build.appstudio.openshift.io/component=" + componentName,
		}}
		k8sClient.List(context.Background(), &pipelineRuns, &labelSelectors)
		return len(pipelineRuns.Items) > 0
	}, timeout, interval).Should(BeTrue())

	return pipelineRuns.Items[0]
}

func fetchTriggerTemplate(componentName string, componentNamespace string) triggersapi.TriggerTemplate {
	triggerTemplate := triggersapi.TriggerTemplate{}

	lookupKey := types.NamespacedName{Name: componentName, Namespace: componentNamespace}

	Eventually(func() bool {
		k8sClient.Get(context.Background(), lookupKey, &triggerTemplate)
		return triggerTemplate.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())

	return triggerTemplate
}

func fetchPipelineRunFromTriggerTemplate(componentName string, componentNamespace string) tektonapi.PipelineRun {
	triggerTemplate := fetchTriggerTemplate(componentName, componentNamespace)

	pipelineRun := tektonapi.PipelineRun{}
	json.Unmarshal(triggerTemplate.Spec.ResourceTemplates[0].RawExtension.Raw, &pipelineRun)

	return pipelineRun
}

var _ = Describe("Component build controller", func() {
	const (
		HASAppName      = "test-application"
		HASCompName     = "test-component"
		HASAppNamespace = "default"
		DisplayName     = "petclinic"
		Description     = "Simple petclinic app"
		ComponentName   = "backend"
		SampleRepoLink  = "https://github.com/devfile-samples/devfile-sample-java-springboot-basic"
	)

	Context("Test build trigger", func() {
		var (
			// All related to the component resources have the same key (but different type)
			resourceKey    = types.NamespacedName{Name: HASCompName, Namespace: HASAppNamespace}
			createdHasComp *appstudiov1alpha1.Component
		)

		createSampleComponent := func() {
			hasComp := &appstudiov1alpha1.Component{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      HASCompName,
					Namespace: HASAppNamespace,
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName: ComponentName,
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
			Expect(k8sClient.Create(ctx, hasComp)).Should(Succeed())
		}

		listCreatedHasCompPipelienRuns := func() *tektonapi.PipelineRunList {
			pipelineRuns := &tektonapi.PipelineRunList{}
			labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
				LabelSelector: "build.appstudio.openshift.io/component=" + createdHasComp.Name,
			}}
			err := k8sClient.List(ctx, pipelineRuns, &labelSelectors)
			Expect(err).ToNot(HaveOccurred())
			return pipelineRuns
		}

		_ = BeforeEach(func() {
			createAndFetchSimpleApp(HASAppName, HASAppNamespace, DisplayName, Description)
			createSampleComponent()

			createdHasComp = &appstudiov1alpha1.Component{}
			Eventually(func() bool {
				k8sClient.Get(ctx, resourceKey, createdHasComp)
				return createdHasComp.ResourceVersion != ""
			}, timeout, interval).Should(BeTrue())
		}, 30)

		_ = AfterEach(func() {
			deleteHASCompCR(resourceKey)
			deleteHASAppCR(types.NamespacedName{Name: HASAppName, Namespace: HASAppNamespace})
		}, 30)

		checkInitialBuildWasSubmitted := func() {
			// Check that a new TriggerTemplate created
			Eventually(func() bool {
				triggerTemplate := &triggersapi.TriggerTemplate{}
				k8sClient.Get(ctx, resourceKey, triggerTemplate)
				return triggerTemplate.ResourceVersion != "" && isOwnedBy(triggerTemplate.GetOwnerReferences(), *createdHasComp)
			}, timeout, interval).Should(BeTrue())

			// Check that build is submitted
			Eventually(func() bool {
				return len(listCreatedHasCompPipelienRuns().Items) > 0
			}, timeout, interval).Should(BeTrue())
		}

		It("should submit a new build if no trigger template found", func() {
			checkInitialBuildWasSubmitted()
		})

		It("should submit a new build if build parameter changed", func() {
			checkInitialBuildWasSubmitted()

			// Update build parameter in the TriggerTemplate
			triggerTemplate := &triggersapi.TriggerTemplate{}
			err := k8sClient.Get(ctx, resourceKey, triggerTemplate)
			Expect(err).ToNot(HaveOccurred())

			triggerTemplate.Spec.Params[0].Name = "new-param"

			err = k8sClient.Update(ctx, triggerTemplate)
			Expect(err).ToNot(HaveOccurred())

			// Check that a new build is submitted
			Eventually(func() bool {
				return len(listCreatedHasCompPipelienRuns().Items) > 1
			}, timeout, interval).Should(BeTrue())
		})

		It("should submit a new build if build trigger resource template changed", func() {
			checkInitialBuildWasSubmitted()

			// Update TriggerResourceTemplate in the TriggerTemplate
			triggerTemplate := &triggersapi.TriggerTemplate{}
			err := k8sClient.Get(ctx, resourceKey, triggerTemplate)
			Expect(err).ToNot(HaveOccurred())

			rawTriggerResourceTemplate := triggerTemplate.Spec.ResourceTemplates[0].Raw
			var triggerResourceTemplate tektonapi.PipelineRun
			err = json.Unmarshal(rawTriggerResourceTemplate, &triggerResourceTemplate)
			Expect(err).ToNot(HaveOccurred())

			triggerResourceTemplate.GenerateName = "test-"

			rawTriggerResourceTemplate, err = json.Marshal(triggerResourceTemplate)
			Expect(err).ToNot(HaveOccurred())
			triggerTemplate.Spec.ResourceTemplates[0].Raw = rawTriggerResourceTemplate
			err = k8sClient.Update(ctx, triggerTemplate)
			Expect(err).ToNot(HaveOccurred())

			// Check that a new build is submitted
			Eventually(func() bool {
				return len(listCreatedHasCompPipelienRuns().Items) > 1
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("Check if build objects are created", func() {
		It("should create build objects even if output image is not provided", func() {
			HASAppNameForBuild := "test-application-1234"
			HASCompNameForBuild := "test-component-1234"

			Expect(k8sClient.Create(context.TODO(), &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "doesmatter",
					Namespace: HASAppNamespace}})).Should(Succeed())

			createAndFetchSimpleApp(HASAppNameForBuild, HASAppNamespace, DisplayName, Description)

			hasComp := &appstudiov1alpha1.Component{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      HASCompNameForBuild,
					Namespace: HASAppNamespace,
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName: ComponentName,
					Application:   HASAppNameForBuild,
					Source: appstudiov1alpha1.ComponentSource{
						ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
							GitSource: &appstudiov1alpha1.GitSource{
								URL:    SampleRepoLink,
								Secret: "doesmatter",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, hasComp)).Should(Succeed())

			hasCompLookupKey := types.NamespacedName{Name: HASCompNameForBuild, Namespace: HASAppNamespace}
			createdHasComp := &appstudiov1alpha1.Component{}
			Eventually(func() bool {
				k8sClient.Get(ctx, hasCompLookupKey, createdHasComp)
				return len(createdHasComp.Status.Conditions) > 0 && createdHasComp.ResourceVersion != ""
			}, timeout, interval).Should(BeTrue())

			// Make sure the devfile model was properly set in Component
			Expect(createdHasComp.Status.Devfile).Should(Not(Equal("")))

			hasAppLookupKey := types.NamespacedName{Name: HASAppNameForBuild, Namespace: HASAppNamespace}
			createdHasApp := &appstudiov1alpha1.Application{}
			Eventually(func() bool {
				k8sClient.Get(ctx, hasAppLookupKey, createdHasApp)
				return len(createdHasApp.Status.Conditions) > 0 && strings.Contains(createdHasApp.Status.Devfile, ComponentName)
			}, timeout, interval).Should(BeTrue())

			pvcLookupKey := types.NamespacedName{Name: "appstudio", Namespace: HASAppNamespace}
			pvc := &corev1.PersistentVolumeClaim{}

			buildResourceName := types.NamespacedName{Name: HASCompNameForBuild, Namespace: HASAppNamespace}

			triggerTemplate := &triggersapi.TriggerTemplate{}
			eventListener := &triggersapi.EventListener{}
			pipelineRuns := tektonapi.PipelineRunList{}
			route := &routev1.Route{}

			Eventually(func() bool {
				k8sClient.Get(ctx, pvcLookupKey, pvc)
				return pvc.Status.Phase == "Pending"
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				fmt.Println(createdHasComp.ResourceVersion)
				labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
					LabelSelector: "build.appstudio.openshift.io/component=" + createdHasComp.Name,
				}}
				k8sClient.List(ctx, &pipelineRuns, &labelSelectors)
				fmt.Println(pipelineRuns.Items)
				return len(pipelineRuns.Items) > 0
			}, timeout, interval).Should(BeTrue())

			pipelineRun := pipelineRuns.Items[0]

			Expect(pipelineRun.Spec.Params).To(Not(BeEmpty()))
			for _, p := range pipelineRun.Spec.Params {
				if p.Name == "output-image" {
					Expect(p.Value.StringVal).To(Equal("docker.io/foo/customized:default-test-component-1234"))
				}
				if p.Name == "git-url" {
					Expect(p.Value.StringVal).To(Equal(SampleRepoLink))
				}
			}

			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal("quay.io/redhat-appstudio/build-templates-bundle:8201a567956ba6d2095d615ea2c0f6ab35f9ba5f"))

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

			Eventually(func() bool {
				k8sClient.Get(ctx, buildResourceName, triggerTemplate)
				return triggerTemplate.ResourceVersion != "" && isOwnedBy(triggerTemplate.GetOwnerReferences(), *createdHasComp)
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				k8sClient.Get(ctx, buildResourceName, eventListener)
				return eventListener.ResourceVersion != "" && isOwnedBy(eventListener.GetOwnerReferences(), *createdHasComp)
			}, timeout, interval).Should(BeTrue())

			Expect(eventListener.Spec.Triggers[0].Bindings[0].Ref).To(Equal("github-push"))
			Expect(eventListener.Spec.Triggers[0].Template.Ref).To(Equal(&HASCompNameForBuild))

			routeResourcename := types.NamespacedName{Namespace: buildResourceName.Namespace, Name: "el" + buildResourceName.Name}
			Eventually(func() bool {
				k8sClient.Get(ctx, routeResourcename, route)
				return route.ResourceVersion != "" && isOwnedBy(route.GetOwnerReferences(), *createdHasComp)
			}, timeout, interval).Should(BeTrue())

			Expect(route.Spec.To.Name).To(Equal("el-" + HASCompNameForBuild))
			Expect(route.Spec.To.Kind).To(Equal("Service"))
			Expect(route.Spec.Path).To(Equal("/"))

			var port int32 = 8080
			Expect(route.Spec.Port.TargetPort.IntVal).To(Equal(port))

			// set the route ingress
			route.Annotations = map[string]string{
				"test": "test",
			}
			route.Status.Ingress = []routev1.RouteIngress{
				{
					Host: "fakeroute.fakeurl.com",
				},
			}

			// Update the route and wait for it to get updated server-side
			Expect(k8sClient.Status().Update(ctx, route)).Should(Succeed())
			Eventually(func() bool {
				k8sClient.Get(ctx, routeResourcename, route)
				return len(route.Status.Ingress) > 0
			}, timeout, interval).Should(BeTrue())

			// Trigger another reconcile in the controller
			// With the Route URL now set, validate that the component's status.webhook is set to the route url
			createdHasComp.Spec.Build.ContainerImage = "newimage"
			Expect(k8sClient.Update(ctx, createdHasComp)).Should(Succeed())

			Eventually(func() bool {
				k8sClient.Get(ctx, hasCompLookupKey, createdHasComp)
				return createdHasComp.Status.Webhook != ""
			}, timeout, interval).Should(BeTrue())

			// Validate that the pipeline service account has been linked with the
			// Github authentication credentials.

			var pipelineSA corev1.ServiceAccount
			secretFound := false
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "pipeline", Namespace: HASAppNamespace}, &pipelineSA)).Should(BeNil())
			for _, secret := range pipelineSA.Secrets {
				if secret.Name == "doesmatter" {
					secretFound = true
					break
				}
				// check if the secret has been annotated
			}
			Expect(secretFound).To(BeTrue())

			retrievedSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "doesmatter", Namespace: HASAppNamespace}, retrievedSecret)).Should(BeNil())
			tektonAnnotation := retrievedSecret.ObjectMeta.Annotations["tekton.dev/git-0"]
			Expect(tektonAnnotation).To(Equal("https://github.com"))

			// Update the Component CR to trigger the reconciler
			retrievedComponent := appstudiov1alpha1.Component{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: HASCompNameForBuild, Namespace: HASAppNamespace}, &retrievedComponent)).Should(BeNil())

			retrievedComponent.Spec.Source.GitSource.URL = "https://github.com/something-else"
			Expect(k8sClient.Update(ctx, &retrievedComponent))

			// confirm that the secret remains annotated.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "doesmatter", Namespace: HASAppNamespace}, retrievedSecret)).Should(BeNil())
			Expect(tektonAnnotation).To(Equal("https://github.com"))

			// confirm that the service account has exactly 1 entry for the secret
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "pipeline", Namespace: HASAppNamespace}, &pipelineSA)).Should(BeNil())
			foundCount := 0
			for _, secret := range pipelineSA.Secrets {
				if secret.Name == "doesmatter" {
					foundCount++
				}
			}
			Expect(foundCount).To(Equal(1))

			// Delete the specified HASComp resource
			deleteHASCompCR(hasCompLookupKey)

			// Delete the specified HASApp resource
			deleteHASAppCR(hasAppLookupKey)
		})

		It("should create build objects when output image is provided", func() {
			HASAppNameForBuild := "test-application-build-without-image"
			HASCompNameForBuild := "test-application-build-without-image"

			createAndFetchSimpleApp(HASAppNameForBuild, HASAppNamespace, DisplayName, Description)

			hasComp := &appstudiov1alpha1.Component{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      HASCompNameForBuild,
					Namespace: HASAppNamespace,
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					ComponentName: ComponentName,
					Application:   HASAppNameForBuild,
					Source: appstudiov1alpha1.ComponentSource{
						ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
							GitSource: &appstudiov1alpha1.GitSource{
								URL: SampleRepoLink,
							},
						},
					},
					Build: appstudiov1alpha1.Build{
						ContainerImage: "docker.io/foo/bar",
					},
				},
			}
			Expect(k8sClient.Create(ctx, hasComp)).Should(Succeed())

			hasCompLookupKey := types.NamespacedName{Name: HASCompNameForBuild, Namespace: HASAppNamespace}
			createdHasComp := &appstudiov1alpha1.Component{}
			Eventually(func() bool {
				k8sClient.Get(ctx, hasCompLookupKey, createdHasComp)
				return len(createdHasComp.Status.Conditions) > 0
			}, timeout, interval).Should(BeTrue())

			// Make sure the devfile model was properly set in Component
			Expect(createdHasComp.Status.Devfile).Should(Not(Equal("")))

			hasAppLookupKey := types.NamespacedName{Name: HASAppNameForBuild, Namespace: HASAppNamespace}
			createdHasApp := &appstudiov1alpha1.Application{}
			Eventually(func() bool {
				k8sClient.Get(ctx, hasAppLookupKey, createdHasApp)
				return len(createdHasApp.Status.Conditions) > 0 && strings.Contains(createdHasApp.Status.Devfile, ComponentName)
			}, timeout, interval).Should(BeTrue())

			pipelineRuns := &tektonapi.PipelineRunList{}

			Eventually(func() bool {
				labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
					LabelSelector: "build.appstudio.openshift.io/component=" + createdHasComp.Name,
				}}
				k8sClient.List(ctx, pipelineRuns, &labelSelectors)
				return len(pipelineRuns.Items) > 0
			}, timeout, interval).Should(BeTrue())

			pipelineRun := pipelineRuns.Items[0]

			Expect(pipelineRun.Spec.Params).To(Not(BeEmpty()))

			for _, p := range pipelineRun.Spec.Params {
				if p.Name == "output-image" {
					Expect(p.Value.StringVal).To(Equal("docker.io/foo/bar"))
				}
			}

			// Delete the specified HASComp resource
			deleteHASCompCR(hasCompLookupKey)

			// Delete the specified HASApp resource
			deleteHASAppCR(hasAppLookupKey)
		})
	})

	Context("Resolve the correct build bundle during the component's creation", func() {
		It("should use the build bundle specified if a configmap is set in the current namespace", func() {
			HASAppNameForBuild := "test-application-custom-bundle"
			HASCompNameForBuild := "test-component-custom-bundle"
			customBuildBundle := "quay.io/custom-bundle/bundle:latest"

			bundleConfigMapData := map[string]string{
				gitopsprepare.BuildBundleConfigMapKey: customBuildBundle,
			}

			configMap := createAndFetchConfigMap(gitopsprepare.BuildBundleConfigMapName, HASAppNamespace, bundleConfigMapData)
			app := createAndFetchSimpleApp(HASAppNameForBuild, HASAppNamespace, DisplayName, Description)
			component := createAndFetchComponent(HASCompNameForBuild, HASAppNamespace, HASAppNameForBuild, ComponentName, SampleRepoLink)
			pipelineRun := fetchPipelineRun(HASCompNameForBuild)
			triggerTemplatePipelineRun := fetchPipelineRunFromTriggerTemplate(HASCompNameForBuild, HASAppNamespace)

			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(customBuildBundle))
			Expect(triggerTemplatePipelineRun.Spec.PipelineRef.Bundle).To(Equal(customBuildBundle))

			deleteHASCompCR(types.NamespacedName{Name: component.Name, Namespace: component.Namespace})
			deleteHASAppCR(types.NamespacedName{Name: app.Name, Namespace: app.Namespace})
			deleteConfigMap(types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace})
		})

		It("should use the build bundle specified if a configmap is set in the default bundle namespace", func() {
			HASAppNameForBuild := "test-application-default-bundle"
			HASCompNameForBuild := "test-component-default-bundle"
			customBuildBundle := "quay.io/default-bundle/bundle:latest"

			bundleConfigMapData := map[string]string{
				gitopsprepare.BuildBundleConfigMapKey: customBuildBundle,
			}

			//create default build bundle namespace
			Expect(k8sClient.Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: gitopsprepare.BuildBundleDefaultNamepace,
				},
			})).Should(Succeed())

			configMap := createAndFetchConfigMap(gitopsprepare.BuildBundleConfigMapName, gitopsprepare.BuildBundleDefaultNamepace, bundleConfigMapData)
			app := createAndFetchSimpleApp(HASAppNameForBuild, HASAppNamespace, DisplayName, Description)
			component := createAndFetchComponent(HASCompNameForBuild, HASAppNamespace, HASAppNameForBuild, ComponentName, SampleRepoLink)
			pipelineRun := fetchPipelineRun(HASCompNameForBuild)
			triggerTemplatePipelineRun := fetchPipelineRunFromTriggerTemplate(HASCompNameForBuild, HASAppNamespace)

			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(customBuildBundle))
			Expect(triggerTemplatePipelineRun.Spec.PipelineRef.Bundle).To(Equal(customBuildBundle))

			deleteHASCompCR(types.NamespacedName{Name: component.Name, Namespace: component.Namespace})
			deleteHASAppCR(types.NamespacedName{Name: app.Name, Namespace: app.Namespace})
			deleteConfigMap(types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace})
		})

		It("should use the fallback build bundle in case no configmap is found", func() {
			HASAppNameForBuild := "test-application-fallback-bundle-2"
			HASCompNameForBuild := "test-component-fallback-bundle-2"

			createAndFetchSimpleApp(HASAppNameForBuild, HASAppNamespace, DisplayName, Description)
			component := createAndFetchComponent(HASCompNameForBuild, HASAppNamespace, HASAppNameForBuild, ComponentName, SampleRepoLink)
			pipelineRun := fetchPipelineRun(HASCompNameForBuild)
			triggerTemplatePipelineRun := fetchPipelineRunFromTriggerTemplate(HASCompNameForBuild, HASAppNamespace)

			Expect(pipelineRun.Spec.PipelineRef.Bundle).To(Equal(gitopsprepare.FallbackBuildBundle))
			Expect(triggerTemplatePipelineRun.Spec.PipelineRef.Bundle).To(Equal(gitopsprepare.FallbackBuildBundle))

			deleteHASCompCR(types.NamespacedName{Name: component.Name, Namespace: component.Namespace})
			deleteHASAppCR(types.NamespacedName{Name: HASAppNameForBuild, Namespace: HASAppNamespace})
		})
	})
})

// Single functions tests

func TestGetGitProvider(t *testing.T) {
	type args struct {
		ctx    context.Context
		gitURL string
	}
	tests := []struct {
		name       string
		args       args
		wantErr    bool
		wantString string
	}{
		{
			name: "github",
			args: args{
				ctx:    context.Background(),
				gitURL: "git@github.com:redhat-appstudio/application-service.git",
			},
			wantErr:    true, //unsupported
			wantString: "",
		},
		{
			name: "github https",
			args: args{
				ctx:    context.Background(),
				gitURL: "https://github.com/redhat-appstudio/application-service.git",
			},
			wantErr:    false,
			wantString: "https://github.com",
		},
		{
			name: "bitbucket https",
			args: args{
				ctx:    context.Background(),
				gitURL: "https://sbose78@bitbucket.org/sbose78/appstudio.git",
			},
			wantErr:    false,
			wantString: "https://bitbucket.org",
		},
		{
			name: "no scheme",
			args: args{
				ctx:    context.Background(),
				gitURL: "github.com/redhat-appstudio/application-service.git",
			},
			wantErr:    true, //fully qualified URL is a must
			wantString: "",
		},
		{
			name: "invalid url",
			args: args{
				ctx:    context.Background(),
				gitURL: "not-even-a-url",
			},
			wantErr:    true, //fully qualified URL is a must
			wantString: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := getGitProvider(tt.args.gitURL); (got != tt.wantString) ||
				(tt.wantErr == true && err == nil) ||
				(tt.wantErr == false && err != nil) {
				t.Errorf("UpdateServiceAccountIfSecretNotLinked() Got Error: = %v, want %v ; Got String:  = %v , want %v", err, tt.wantErr, got, tt.wantString)
			}
		})
	}
}

func TestUpdateServiceAccountIfSecretNotLinked(t *testing.T) {
	type args struct {
		gitSecretName  string
		serviceAccount *corev1.ServiceAccount
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "present",
			args: args{
				gitSecretName: "present",
				serviceAccount: &corev1.ServiceAccount{
					Secrets: []corev1.ObjectReference{
						{
							Name: "present",
						},
					},
				},
			},
			want: false, // since it was present, this implies the SA wasn't updated.
		},
		{
			name: "not present",
			args: args{
				gitSecretName: "not-present",
				serviceAccount: &corev1.ServiceAccount{
					Secrets: []corev1.ObjectReference{
						{
							Name: "something-else",
						},
					},
				},
			},
			want: true, // since it wasn't present, this implies the SA was updated.
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := updateServiceAccountIfSecretNotLinked(tt.args.gitSecretName, tt.args.serviceAccount); got != tt.want {
				t.Errorf("UpdateServiceAccountIfSecretNotLinked() = %v, want %v", got, tt.want)
			}
		})
	}
}
