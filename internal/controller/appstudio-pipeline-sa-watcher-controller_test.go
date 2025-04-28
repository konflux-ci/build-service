/*
Copyright 2022-2025 Red Hat, Inc.

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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	imgcv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ = Describe("appstudio-pipeline Service Account watcher controller", func() {

	var (
		namespace              = "sa-sync"
		component1Key          = types.NamespacedName{Name: HASCompName + "-sa-watcher-1", Namespace: namespace}
		component2Key          = types.NamespacedName{Name: HASCompName + "-sa-watcher-2", Namespace: namespace}
		appstudioPipelineSAKey = types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: namespace}
		component1SAKey        = types.NamespacedName{Name: "build-pipeline-" + component1Key.Name, Namespace: namespace}
		component2SAKey        = types.NamespacedName{Name: "build-pipeline-" + component2Key.Name, Namespace: namespace}

		commonSecret1Name     = "common-secret-1"
		commonSecret2Name     = "common-secret-2"
		commonPullSecret1Name = "common-pull-secret-1"
		commonPullSecret2Name = "common-pull-secret-2"
	)

	Context("Test SA secrets sync", func() {

		It("prepare resources", func() {
			createNamespace(namespace)

			component1 := getComponentData(componentConfig{componentKey: component1Key})
			component1.Spec.ContainerImage = ""
			Expect(k8sClient.Create(ctx, component1)).To(Succeed())
			component2 := getComponentData(componentConfig{componentKey: component2Key})
			component2.Spec.ContainerImage = ""
			Expect(k8sClient.Create(ctx, component2)).To(Succeed())

			appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
			Expect(appstudioPipelineSA.Secrets).To(HaveLen(0))
			Expect(appstudioPipelineSA.ImagePullSecrets).To(HaveLen(0))
			component1SA := waitServiceAccount(component1SAKey)
			Expect(component1SA.Secrets).To(HaveLen(0))
			Expect(component1SA.ImagePullSecrets).To(HaveLen(0))
			component2SA := waitServiceAccount(component1SAKey)
			Expect(component2SA.Secrets).To(HaveLen(0))
			Expect(component2SA.ImagePullSecrets).To(HaveLen(0))
		})

		It("should sync linked secret", func() {
			appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
			appstudioPipelineSA.Secrets = append(appstudioPipelineSA.Secrets, corev1.ObjectReference{Name: commonSecret1Name, Namespace: namespace})
			Expect(k8sClient.Update(ctx, &appstudioPipelineSA)).To(Succeed())
			Eventually(func() bool {
				appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
				return len(appstudioPipelineSA.Secrets) == 1 && appstudioPipelineSA.Secrets[0].Name == commonSecret1Name
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				component1SA := waitServiceAccount(component1SAKey)
				return len(component1SA.Secrets) == 1 && component1SA.Secrets[0].Name == commonSecret1Name && len(component1SA.ImagePullSecrets) == 0
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				component2SA := waitServiceAccount(component2SAKey)
				return len(component2SA.Secrets) == 1 && component2SA.Secrets[0].Name == commonSecret1Name && len(component2SA.ImagePullSecrets) == 0
			}, timeout, interval).Should(BeTrue())
		})

		It("should sync linked pull secret", func() {
			appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
			appstudioPipelineSA.ImagePullSecrets = append(appstudioPipelineSA.ImagePullSecrets, corev1.LocalObjectReference{Name: commonPullSecret1Name})
			Expect(k8sClient.Update(ctx, &appstudioPipelineSA)).To(Succeed())
			Eventually(func() bool {
				appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
				return len(appstudioPipelineSA.ImagePullSecrets) == 1 && appstudioPipelineSA.ImagePullSecrets[0].Name == commonPullSecret1Name
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				component1SA := waitServiceAccount(component1SAKey)
				return len(component1SA.ImagePullSecrets) == 1 && component1SA.ImagePullSecrets[0].Name == commonPullSecret1Name &&
					len(component1SA.Secrets) == 1 && component1SA.Secrets[0].Name == commonSecret1Name
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				component2SA := waitServiceAccount(component2SAKey)
				return len(component2SA.ImagePullSecrets) == 1 && component2SA.ImagePullSecrets[0].Name == commonPullSecret1Name &&
					len(component2SA.Secrets) == 1 && component2SA.Secrets[0].Name == commonSecret1Name
			}, timeout, interval).Should(BeTrue())
		})

		It("should sync both linked secrets and pull secrets", func() {
			appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
			appstudioPipelineSA.Secrets = append(appstudioPipelineSA.Secrets, corev1.ObjectReference{Name: commonSecret2Name})
			appstudioPipelineSA.ImagePullSecrets = append(appstudioPipelineSA.ImagePullSecrets, corev1.LocalObjectReference{Name: commonPullSecret2Name})
			Expect(k8sClient.Update(ctx, &appstudioPipelineSA)).To(Succeed())
			Eventually(func() bool {
				appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
				return len(appstudioPipelineSA.Secrets) == 2 && len(appstudioPipelineSA.ImagePullSecrets) == 2
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				component1SA := waitServiceAccount(component1SAKey)
				return len(component1SA.Secrets) == 2 &&
					component1SA.Secrets[0].Name == commonSecret1Name && component1SA.Secrets[1].Name == commonSecret2Name &&
					len(component1SA.ImagePullSecrets) == 2 &&
					component1SA.ImagePullSecrets[0].Name == commonPullSecret1Name && component1SA.ImagePullSecrets[1].Name == commonPullSecret2Name
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				component2SA := waitServiceAccount(component2SAKey)
				return len(component2SA.Secrets) == 2 &&
					component2SA.Secrets[0].Name == commonSecret1Name && component2SA.Secrets[1].Name == commonSecret2Name &&
					len(component2SA.ImagePullSecrets) == 2 &&
					component2SA.ImagePullSecrets[0].Name == commonPullSecret1Name && component2SA.ImagePullSecrets[1].Name == commonPullSecret2Name
			}, timeout, interval).Should(BeTrue())
		})

		It("should sync a few linked secret", func() {
			commonSecret3Name := "common-secret-3"
			commonSecret4Name := "common-secret-4"
			appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
			appstudioPipelineSA.Secrets = append(appstudioPipelineSA.Secrets, corev1.ObjectReference{Name: commonSecret3Name})
			appstudioPipelineSA.Secrets = append(appstudioPipelineSA.Secrets, corev1.ObjectReference{Name: commonSecret4Name})
			Expect(k8sClient.Update(ctx, &appstudioPipelineSA)).To(Succeed())
			Eventually(func() bool {
				appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
				return len(appstudioPipelineSA.Secrets) == 4 && len(appstudioPipelineSA.ImagePullSecrets) == 2
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				component1SA := waitServiceAccount(component1SAKey)
				return len(component1SA.Secrets) == 4 &&
					component1SA.Secrets[0].Name == commonSecret1Name && component1SA.Secrets[1].Name == commonSecret2Name &&
					component1SA.Secrets[2].Name == commonSecret3Name && component1SA.Secrets[3].Name == commonSecret4Name &&
					len(component1SA.ImagePullSecrets) == 2 &&
					component1SA.ImagePullSecrets[0].Name == commonPullSecret1Name && component1SA.ImagePullSecrets[1].Name == commonPullSecret2Name
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				component2SA := waitServiceAccount(component2SAKey)
				return len(component2SA.Secrets) == 4 &&
					component2SA.Secrets[0].Name == commonSecret1Name && component2SA.Secrets[1].Name == commonSecret2Name &&
					component2SA.Secrets[2].Name == commonSecret3Name && component2SA.Secrets[3].Name == commonSecret4Name &&
					len(component2SA.ImagePullSecrets) == 2 &&
					component2SA.ImagePullSecrets[0].Name == commonPullSecret1Name && component2SA.ImagePullSecrets[1].Name == commonPullSecret2Name
			}, timeout, interval).Should(BeTrue())
		})

		It("should ignore dockercfg secret of appstudio-pipeline", func() {
			appstudioPipelineDockercfgSecretName := "appstudio-pipeline-dockercfg-c66gp"

			appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
			appstudioPipelineSA.Secrets = append(appstudioPipelineSA.Secrets, corev1.ObjectReference{Name: appstudioPipelineDockercfgSecretName})
			appstudioPipelineSA.ImagePullSecrets = append(appstudioPipelineSA.ImagePullSecrets, corev1.LocalObjectReference{Name: appstudioPipelineDockercfgSecretName})
			Expect(k8sClient.Update(ctx, &appstudioPipelineSA)).To(Succeed())

			Consistently(func() bool {
				component1SA := waitServiceAccount(component1SAKey)
				component2SA := waitServiceAccount(component2SAKey)
				return !isSecretLinkedToServiceAccount(component1SA, appstudioPipelineDockercfgSecretName, false) &&
					!isSecretLinkedToServiceAccount(component1SA, appstudioPipelineDockercfgSecretName, true) &&
					!isSecretLinkedToServiceAccount(component2SA, appstudioPipelineDockercfgSecretName, false) &&
					!isSecretLinkedToServiceAccount(component2SA, appstudioPipelineDockercfgSecretName, true)
			}, timeout, interval).Should(BeTrue())
		})

		It("should remove dockercfg secret of appstudio-pipeline from dedicated Service Account", func() {
			appstudioPipelineDockercfgSecretName := "appstudio-pipeline-dockercfg-c66gp"
			buildPipelineSaName := component1Key.Name + "-dockercfg-zb8kc"

			component1SA := waitServiceAccount(component1SAKey)
			component1SA.Secrets = append(component1SA.Secrets, corev1.ObjectReference{Name: appstudioPipelineDockercfgSecretName})
			component1SA.Secrets = append(component1SA.Secrets, corev1.ObjectReference{Name: buildPipelineSaName})
			component1SA.ImagePullSecrets = append(component1SA.ImagePullSecrets, corev1.LocalObjectReference{Name: appstudioPipelineDockercfgSecretName})
			component1SA.ImagePullSecrets = append(component1SA.ImagePullSecrets, corev1.LocalObjectReference{Name: buildPipelineSaName})
			Expect(k8sClient.Update(ctx, &component1SA)).To(Succeed())

			// Trigger reconcile
			appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
			appstudioPipelineSA.Annotations = map[string]string{}
			appstudioPipelineSA.Annotations["test"] = "test"
			Expect(k8sClient.Update(ctx, &appstudioPipelineSA)).To(Succeed())

			Eventually(func() bool {
				component1SA := waitServiceAccount(component1SAKey)
				return !isSecretLinkedToServiceAccount(component1SA, appstudioPipelineDockercfgSecretName, false) &&
					!isSecretLinkedToServiceAccount(component1SA, appstudioPipelineDockercfgSecretName, true) &&
					isSecretLinkedToServiceAccount(component1SA, buildPipelineSaName, false) &&
					isSecretLinkedToServiceAccount(component1SA, buildPipelineSaName, true)
			}, timeout, interval).Should(BeTrue())
		})

		It("should sync only image repository secret that is not component related", func() {
			component1ImageRepositoryPushSecretName := "component1-image-push"
			component1ImageRepositoryPullSecretName := "component1-image-pull"
			commonImageRepositoryPushSecretName := "common-image-push"
			commonImageRepositoryPullSecretName := "common-image-pull"

			component1 := getComponent(component1Key)
			component1ImageRepository := &imgcv1alpha1.ImageRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "imagerepository-for-component1",
					Namespace: namespace,
				},
			}
			Expect(controllerutil.SetOwnerReference(component1, component1ImageRepository, scheme.Scheme)).To(Succeed())
			Expect(k8sClient.Create(ctx, component1ImageRepository)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component1ImageRepository.Name, Namespace: namespace}, component1ImageRepository)).To(Succeed())
			component1ImageRepository.Status = imgcv1alpha1.ImageRepositoryStatus{
				Credentials: imgcv1alpha1.CredentialsStatus{
					PushSecretName: component1ImageRepositoryPushSecretName,
					PullSecretName: component1ImageRepositoryPullSecretName,
				},
			}
			Expect(k8sClient.Status().Update(ctx, component1ImageRepository)).To(Succeed())
			defer func() { Expect(k8sClient.Delete(ctx, component1ImageRepository)).To(Succeed()) }()

			commonImageRepository := &imgcv1alpha1.ImageRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "common",
					Namespace: namespace,
				},
			}
			Expect(k8sClient.Create(ctx, commonImageRepository)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: commonImageRepository.Name, Namespace: namespace}, commonImageRepository)).To(Succeed())
			commonImageRepository.Status = imgcv1alpha1.ImageRepositoryStatus{
				Credentials: imgcv1alpha1.CredentialsStatus{
					PushSecretName: commonImageRepositoryPushSecretName,
					PullSecretName: commonImageRepositoryPullSecretName,
				},
			}
			Expect(k8sClient.Status().Update(ctx, commonImageRepository)).To(Succeed())
			defer func() { Expect(k8sClient.Delete(ctx, commonImageRepository)).To(Succeed()) }()

			appstudioPipelineSA := waitServiceAccount(appstudioPipelineSAKey)
			appstudioPipelineSA.Secrets = append(appstudioPipelineSA.Secrets, corev1.ObjectReference{Name: component1ImageRepositoryPushSecretName})
			appstudioPipelineSA.Secrets = append(appstudioPipelineSA.Secrets, corev1.ObjectReference{Name: commonImageRepositoryPushSecretName})
			appstudioPipelineSA.ImagePullSecrets = append(appstudioPipelineSA.ImagePullSecrets, corev1.LocalObjectReference{Name: component1ImageRepositoryPullSecretName})
			appstudioPipelineSA.ImagePullSecrets = append(appstudioPipelineSA.ImagePullSecrets, corev1.LocalObjectReference{Name: commonImageRepositoryPullSecretName})
			Expect(k8sClient.Update(ctx, &appstudioPipelineSA)).To(Succeed())

			Eventually(func() bool {
				component2SA := waitServiceAccount(component2SAKey)
				return !isSecretLinkedToServiceAccount(component2SA, component1ImageRepositoryPushSecretName, false) &&
					!isSecretLinkedToServiceAccount(component2SA, component1ImageRepositoryPullSecretName, true) &&
					isSecretLinkedToServiceAccount(component2SA, commonImageRepositoryPushSecretName, false) &&
					isSecretLinkedToServiceAccount(component2SA, commonImageRepositoryPullSecretName, true)
			}, timeout, interval).Should(BeTrue())
		})

		It("clean up", func() {
			deleteComponent(component1Key)
			deleteComponent(component2Key)
			deleteServiceAccount(appstudioPipelineSAKey)
			deleteServiceAccount(component1Key)
			deleteServiceAccount(component2Key)
		})
	})
})

func isSecretLinkedToServiceAccount(sa corev1.ServiceAccount, secretName string, isPull bool) bool {
	if isPull {
		for _, secretReference := range sa.ImagePullSecrets {
			if secretReference.Name == secretName {
				return true
			}
		}
		return false
	}

	for _, secretReference := range sa.Secrets {
		if secretReference.Name == secretName {
			return true
		}
	}
	return false
}
