/*
Copyright 2022.

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
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/apis/duck/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	appstudiosharedv1alpha1 "github.com/redhat-appstudio/managed-gitops/appstudio-shared/apis/appstudio.redhat.com/v1alpha1"
)

const (
	// timeout is used as a limit until condition become true
	// Usually used in Eventually statements
	timeout = time.Second * 15
	// ensureTimeout is used as a period of time during which the condition should not be changed
	// Usually used in Consistently statements
	ensureTimeout = time.Second * 4
	interval      = time.Millisecond * 250
)

const (
	HASAppName              = "test-application"
	HASCompName             = "test-component"
	HASAppNamespace         = "default"
	SampleRepoLink          = "https://github.com/devfile-samples/devfile-sample-java-springboot-basic"
	GitSecretName           = "git-secret"
	ComponentContainerImage = "registry.io/username/image:tag"
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

func createApplication(resourceKey types.NamespacedName) {
	application := &appstudiov1alpha1.Application{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "appstudio.redhat.com/v1alpha1",
			Kind:       "Application",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceKey.Name,
			Namespace: resourceKey.Namespace,
		},
		Spec: appstudiov1alpha1.ApplicationSpec{
			DisplayName: "test-app",
		},
	}

	Expect(k8sClient.Create(ctx, application)).Should(Succeed())
}

func deleteApplication(resourceKey types.NamespacedName) {
	// Delete
	Eventually(func() error {
		f := &appstudiov1alpha1.Application{}
		Expect(k8sClient.Get(ctx, resourceKey, f)).To(Succeed())
		return k8sClient.Delete(ctx, f)
	}, timeout, interval).Should(Succeed())

	// Wait for delete to finish
	Eventually(func() error {
		f := &appstudiov1alpha1.Component{}
		return k8sClient.Get(ctx, resourceKey, f)
	}, timeout, interval).ShouldNot(Succeed())
}

// createComponent creates sample component resource and verifies it was properly created
func createComponentForPaCBuild(componentLookupKey types.NamespacedName) {
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
						URL: SampleRepoLink,
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, component)).Should(Succeed())

	getComponent(componentLookupKey)
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
	Expect(k8sClient.Create(ctx, component)).Should(Succeed())

	getComponent(componentLookupKey)
}

func getComponent(componentKey types.NamespacedName) *appstudiov1alpha1.Component {
	component := &appstudiov1alpha1.Component{}
	Eventually(func() bool {
		Expect(k8sClient.Get(ctx, componentKey, component)).Should(Succeed())
		return component.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return component
}

// deleteComponent deletes the specified component resource and verifies it was properly deleted
func deleteComponent(componentKey types.NamespacedName) {
	// Delete
	Eventually(func() error {
		f := &appstudiov1alpha1.Component{}
		Expect(k8sClient.Get(ctx, componentKey, f)).To(Succeed())
		return k8sClient.Delete(ctx, f)
	}, timeout, interval).Should(Succeed())

	// Wait for delete to finish
	Eventually(func() error {
		f := &appstudiov1alpha1.Component{}
		return k8sClient.Get(ctx, componentKey, f)
	}, timeout, interval).ShouldNot(Succeed())
}

func setComponentDevfileModel(componentKey types.NamespacedName) {
	component := &appstudiov1alpha1.Component{}
	Eventually(func() error {
		Expect(k8sClient.Get(ctx, componentKey, component)).To(Succeed())
		component.Status.Devfile = "schemaVersion: 2.2.0"
		return k8sClient.Status().Update(ctx, component)
	}, timeout, interval).Should(Succeed())

	component = getComponent(componentKey)
	Expect(component.Status.Devfile).Should(Not(Equal("")))
}

func listApplicationSnapshots(resourceKey types.NamespacedName) []appstudiosharedv1alpha1.ApplicationSnapshot {
	applicationSnapshots := &appstudiosharedv1alpha1.ApplicationSnapshotList{}
	labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
		LabelSelector: ApplicationNameLabelName + "=" + resourceKey.Name,
	}}
	err := k8sClient.List(ctx, applicationSnapshots, &labelSelectors)
	Expect(err).ToNot(HaveOccurred())
	return applicationSnapshots.Items
}

func deleteAllApplicationSnapshots() {
	if err := k8sClient.DeleteAllOf(ctx, &appstudiosharedv1alpha1.ApplicationSnapshot{}, &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{Namespace: HASAppNamespace},
	}); err != nil {
		Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
	}
}

func listComponentInitialPipelineRuns(componentKey types.NamespacedName) *tektonapi.PipelineRunList {
	pipelineRuns := &tektonapi.PipelineRunList{}
	labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
		LabelSelector: ComponentNameLabelName + "=" + componentKey.Name,
	}}
	err := k8sClient.List(ctx, pipelineRuns, &labelSelectors)
	Expect(err).ToNot(HaveOccurred())
	return pipelineRuns
}

func deleteComponentInitialPipelineRuns(componentKey types.NamespacedName) {
	for _, pipelineRun := range listComponentInitialPipelineRuns(componentKey).Items {
		Expect(k8sClient.Delete(ctx, &pipelineRun)).Should(Succeed())
	}
}

func ensureOneInitialPipelineRunCreated(componentKey types.NamespacedName) {
	component := getComponent(componentKey)
	Eventually(func() bool {
		pipelineRuns := listComponentInitialPipelineRuns(componentKey).Items
		if len(pipelineRuns) != 1 {
			return false
		}
		pipelineRun := pipelineRuns[0]
		return isOwnedBy(pipelineRun.OwnerReferences, *component)
	}, timeout, interval).Should(BeTrue())
}

func ensureNoInitialPipelineRunsCreated(componentLookupKey types.NamespacedName) {
	component := getComponent(componentLookupKey)

	pipelineRuns := &tektonapi.PipelineRunList{}
	Consistently(func() bool {
		labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
			LabelSelector: "build.appstudio.openshift.io/component=" + component.Name,
		}}
		Expect(k8sClient.List(ctx, pipelineRuns, &labelSelectors)).To(Succeed())
		return len(pipelineRuns.Items) == 0
	}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
}

func createWebhookPipelineRun(resourceKey types.NamespacedName) {
	pipelineRun := &tektonapi.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceKey.Name,
			Namespace: resourceKey.Namespace,
			Labels: map[string]string{
				ComponentNameLabelName: resourceKey.Name,
			},
		},
	}
	Expect(k8sClient.Create(ctx, pipelineRun)).Should(Succeed())

	Expect(k8sClient.Get(ctx, resourceKey, pipelineRun)).Should(Succeed())
	pipelineRun.Status = tektonapi.PipelineRunStatus{
		Status: v1beta1.Status{
			Conditions: v1beta1.Conditions{
				apis.Condition{
					Reason: "Running",
					Status: "Unknown",
					Type:   apis.ConditionSucceeded,
				},
			},
		},
		PipelineRunStatusFields: tektonapi.PipelineRunStatusFields{
			StartTime: &metav1.Time{
				Time: time.Now(),
			},
		},
	}
	Expect(k8sClient.Status().Update(ctx, pipelineRun)).Should(Succeed())
}

func succeedWebhookPipelineRun(resourceKey types.NamespacedName) {
	pipelineRun := &tektonapi.PipelineRun{}
	Expect(k8sClient.Get(ctx, resourceKey, pipelineRun)).Should(Succeed())
	pipelineRun.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	pipelineRun.Status.Conditions = v1beta1.Conditions{
		apis.Condition{
			Reason: "Completed",
			Status: "True",
			Type:   apis.ConditionSucceeded,
		},
	}
	Expect(k8sClient.Status().Update(ctx, pipelineRun)).Should(Succeed())
}

func failWebhookPipelineRun(resourceKey types.NamespacedName) {
	pipelineRun := &tektonapi.PipelineRun{}
	Expect(k8sClient.Get(ctx, resourceKey, pipelineRun)).Should(Succeed())
	pipelineRun.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	pipelineRun.Status.Conditions = v1beta1.Conditions{
		apis.Condition{
			Reason: "Failed",
			Status: "False",
			Type:   apis.ConditionSucceeded,
		},
	}
	Expect(k8sClient.Status().Update(ctx, pipelineRun)).Should(Succeed())
}

func succeedInitialPipelineRun(componentKey types.NamespacedName) {
	// Put the PipelineRun in runnning state
	pipelineRun := &listComponentInitialPipelineRuns(componentKey).Items[0]
	pipelineRun.Status.StartTime = &metav1.Time{Time: time.Now()}
	pipelineRun.Status.Conditions = v1beta1.Conditions{
		apis.Condition{
			Reason: "Running",
			Status: "Unknown",
			Type:   apis.ConditionSucceeded,
		}}
	Expect(k8sClient.Status().Update(ctx, pipelineRun)).Should(Succeed())

	// Succeed the PipelineRun
	pipelineRun = &listComponentInitialPipelineRuns(componentKey).Items[0]
	pipelineRun.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	pipelineRun.Status.Conditions = v1beta1.Conditions{
		apis.Condition{
			Reason: "Completed",
			Status: "True",
			Type:   apis.ConditionSucceeded,
		},
	}
	Expect(k8sClient.Status().Update(ctx, pipelineRun)).Should(Succeed())
}

// addBuiltComponentImageToBuildPipelineResults adds given image and digest into pipeline results of the latest pipeline run
func addBuiltComponentImageToBuildPipelineResults(componentKey types.NamespacedName, image string, digest string) {
	var buildPipelineResults []tektonapi.PipelineRunResult
	if image != "" {
		buildPipelineResults = append(buildPipelineResults, tektonapi.PipelineRunResult{
			Name:  "IMAGE_URL",
			Value: image,
		})
	}
	if digest != "" {
		buildPipelineResults = append(buildPipelineResults, tektonapi.PipelineRunResult{
			Name:  "IMAGE_DIGEST",
			Value: digest,
		})
	}

	buildPipelineRuns := listComponentInitialPipelineRuns(componentKey)
	Expect(len(buildPipelineRuns.Items) > 0).To(BeTrue())
	buildPipelineRun := &buildPipelineRuns.Items[len(buildPipelineRuns.Items)-1]
	buildPipelineRun.Status.PipelineResults = buildPipelineResults
	Expect(k8sClient.Status().Update(ctx, buildPipelineRun)).Should(Succeed())
}

func deleteAllPipelineRuns() {
	if err := k8sClient.DeleteAllOf(ctx, &tektonapi.PipelineRun{}, &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{Namespace: HASAppNamespace},
	}); err != nil {
		Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
	}
}

func deleteAllTaskRuns() {
	if err := k8sClient.DeleteAllOf(ctx, &tektonapi.TaskRun{}, &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{Namespace: HASAppNamespace},
	}); err != nil {
		Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
	}
}

func createConfigMap(resourceKey types.NamespacedName, data map[string]string) {
	configMap := corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		Data: data,
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceKey.Name,
			Namespace: resourceKey.Namespace,
		},
	}

	Expect(k8sClient.Create(ctx, &configMap)).Should(Succeed())
}

func deleteConfigMap(resourceKey types.NamespacedName) {
	configMap := corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceKey.Name,
			Namespace: resourceKey.Namespace,
		},
	}

	Expect(k8sClient.Delete(ctx, &configMap)).Should(Succeed())
}

func createSecret(resourceKey types.NamespacedName, data map[string]string) {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceKey.Name,
			Namespace: resourceKey.Namespace,
		},
		StringData: data,
	}
	if err := k8sClient.Create(ctx, secret); err != nil {
		if !k8sErrors.IsAlreadyExists(err) {
			Fail(err.Error())
		}
		deleteSecret(resourceKey)
		secret.ResourceVersion = ""
		Expect(k8sClient.Create(ctx, secret)).Should(Succeed())
	}
}

func deleteSecret(resourceKey types.NamespacedName) {
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, resourceKey, secret); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail(err.Error())
	}
	if err := k8sClient.Delete(ctx, secret); err != nil {
		if !k8sErrors.IsNotFound(err) {
			Fail(err.Error())
		}
		return
	}
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, resourceKey, secret))
	}, timeout, interval).Should(BeTrue())
}

func ensureSecretCreated(resourceKey types.NamespacedName) {
	secret := &corev1.Secret{}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, resourceKey, secret)
		return err == nil && secret.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
}

func ensureSecretNotCreated(resourceKey types.NamespacedName) {
	secret := &corev1.Secret{}
	Consistently(func() bool {
		err := k8sClient.Get(ctx, resourceKey, secret)
		return k8sErrors.IsNotFound(err)
	}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
}

func createNamespace(name string) {
	namespace := corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	if err := k8sClient.Create(ctx, &namespace); err != nil && !k8sErrors.IsAlreadyExists(err) {
		Fail(err.Error())
	}
}

func ensurePaCRepositoryCreated(resourceKey types.NamespacedName) {
	pacRepository := &pacv1alpha1.Repository{}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, resourceKey, pacRepository)
		return err == nil && pacRepository.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
}

func ensureComponentInitialBuildAnnotationState(componentKey types.NamespacedName, initialBuildAnnotation bool) {
	if initialBuildAnnotation {
		Eventually(func() bool {
			component := getComponent(componentKey)
			annotations := component.GetAnnotations()
			return annotations != nil && annotations[InitialBuildAnnotationName] == "true"
		}, timeout, interval).Should(BeTrue())
	} else {
		Consistently(func() bool {
			component := getComponent(componentKey)
			annotations := component.GetAnnotations()
			if annotations == nil {
				return true
			}
			return annotations[InitialBuildAnnotationName] != "true"
		}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
	}
}

func createRoute(routeKey types.NamespacedName, host string) {
	route := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeKey.Name,
			Namespace: routeKey.Namespace,
		},
		Spec: routev1.RouteSpec{
			Host: host,
		},
	}

	if err := k8sClient.Create(ctx, &route); err != nil && !k8sErrors.IsAlreadyExists(err) {
		Fail(err.Error())
	}
}

func deleteRoute(routeKey types.NamespacedName) {
	route := &routev1.Route{}
	if err := k8sClient.Get(ctx, routeKey, route); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail(err.Error())
	}
	if err := k8sClient.Delete(ctx, route); err != nil && !k8sErrors.IsNotFound(err) {
		Fail(err.Error())
	}
}
