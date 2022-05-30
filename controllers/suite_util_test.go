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

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/apis/duck/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
)

const (
	timeout  = time.Second * 15
	interval = time.Millisecond * 250
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
		component.Status.Devfile = "version: 2.2.0"
		return k8sClient.Status().Update(ctx, component)
	}, timeout, interval).Should(Succeed())

	component = getComponent(componentKey)
	Expect(component.Status.Devfile).Should(Not(Equal("")))
}

func listComponentInitialPipelineRuns(componentKey types.NamespacedName) *tektonapi.PipelineRunList {
	pipelineRuns := &tektonapi.PipelineRunList{}
	labelSelectors := client.ListOptions{Raw: &metav1.ListOptions{
		LabelSelector: "build.appstudio.openshift.io/component=" + componentKey.Name,
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
	}, timeout, interval).WithTimeout(10 * time.Second).Should(BeTrue())
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

func deleteAllPipelineRuns() {
	Expect(k8sClient.DeleteAllOf(ctx, &tektonapi.PipelineRun{}, &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{Namespace: HASAppNamespace},
	})).Should(Succeed())
}

func createBuildTaskRunWithImage(resourceKey types.NamespacedName, image string) {
	taskRunKey := types.NamespacedName{
		Name:      resourceKey.Name + "-" + BuildImageTaskName + "-random1234",
		Namespace: resourceKey.Namespace,
	}

	taskRun := &tektonapi.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskRunKey.Name,
			Namespace: taskRunKey.Namespace,
			Labels: map[string]string{
				PipelineRunLabelName:  resourceKey.Name,
				PipelineTaskLabelName: BuildImageTaskName,
			},
		},
	}
	Expect(k8sClient.Create(ctx, taskRun)).Should(Succeed())

	Expect(k8sClient.Get(ctx, taskRunKey, taskRun)).Should(Succeed())
	taskRun.Status = tektonapi.TaskRunStatus{
		TaskRunStatusFields: tektonapi.TaskRunStatusFields{
			TaskRunResults: []tektonapi.TaskRunResult{
				{
					Name:  "IMAGE_DIGEST",
					Value: "sha256:71fd928246979eb68fbe12ee60159408f7535f6252335a4d13497e35eb81854f",
				},
				{
					Name:  "IMAGE_URL",
					Value: image,
				},
			},
		},
	}
	Expect(k8sClient.Status().Update(ctx, taskRun)).Should(Succeed())
}

func deleteAllTaskRuns() {
	Expect(k8sClient.DeleteAllOf(ctx, &tektonapi.TaskRun{}, &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{Namespace: HASAppNamespace},
	})).Should(Succeed())
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
