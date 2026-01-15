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
	"encoding/json"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	imgcv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	releaseapi "github.com/konflux-ci/release-service/api/v1alpha1"
	tektonutils "github.com/konflux-ci/release-service/tekton/utils"

	. "github.com/konflux-ci/build-service/pkg/common"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
	"sigs.k8s.io/yaml"
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
	// TODO remove after only new model is used
	HASAppName = "test-application"
	// TODO remove after only new model is used
	HASCompName = "test-component"
	// TODO remove after only new model is used
	HASAppNamespace         = "default"
	DefaultCompName         = "test-component"
	DefaultCompNamespace    = "default"
	SampleRepoLink          = "https://github.com/devfile-samples/devfile-sample-java-springboot-basic"
	ComponentContainerImage = "registry.io/username/image:tag"
	SelectorDefaultName     = "default"
	DefaultRevisionName     = "main"
	DefaultVersionName      = "version1"

	defaultPipelineName   = "docker-build"
	defaultPipelineBundle = "quay.io/redhat-appstudio-tekton-catalog/pipeline-docker-build:07ec767c565b36296b4e185b01f05536848d9c12"

	rpaMappingRepository1 = "test1.registry/publish"
	rpaMappingRepository2 = "test2.registry/publish"
	rpaMappingRepository3 = "test3.registry/publish"
)

var (
	defaultPipelineConfigMapKey = types.NamespacedName{Name: buildPipelineConfigMapResourceName, Namespace: BuildServiceNamespaceName}
)

type componentConfig struct {
	componentKey       types.NamespacedName
	containerImage     string
	versions           []compapiv1alpha1.ComponentVersion
	gitURL             string
	annotations        map[string]string
	finalizers         []string
	actions            compapiv1alpha1.ComponentActions
	repositorySettings compapiv1alpha1.RepositorySettings
	defaultPipeline    compapiv1alpha1.ComponentBuildPipeline
	skipOffboardingPr  bool
}

// TODO remove after only new model is used
type componentConfigOldModel struct {
	componentKey     types.NamespacedName
	containerImage   string
	gitURL           string
	gitRevision      string
	gitSourceContext string
	application      string
	annotations      map[string]string
	finalizers       []string
}

func getComponentData(config componentConfig) *compapiv1alpha1.Component {
	name := config.componentKey.Name
	if name == "" {
		name = DefaultCompName
	}
	namespace := config.componentKey.Namespace
	if namespace == "" {
		namespace = DefaultCompNamespace
	}
	image := config.containerImage
	if image == "" {
		image = ComponentContainerImage
	}
	gitUrl := config.gitURL
	if gitUrl == "" {
		gitUrl = SampleRepoLink + "-" + name
	}
	versions := config.versions
	// Only add default version if versions is nil (not set), not if it's explicitly empty []
	if versions == nil {
		versions = []compapiv1alpha1.ComponentVersion{
			{Name: DefaultVersionName, Revision: DefaultRevisionName, Context: ""},
		}
	}
	annotations := make(map[string]string)
	// Always set new model annotation for components created with getComponentData
	annotations["build.appstudio.openshift.io/component_model_version"] = "v2"
	if config.annotations != nil {
		for key, value := range config.annotations {
			annotations[key] = value
		}
	}
	finalizers := []string{}
	if len(config.finalizers) != 0 {
		finalizers = append(finalizers, config.finalizers...)
	}
	return &compapiv1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "appstudio.redhat.com/v1alpha1",
			Kind:       "Component",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
			Finalizers:  finalizers,
		},
		Spec: compapiv1alpha1.ComponentSpec{
			Actions:              config.actions,
			RepositorySettings:   config.repositorySettings,
			DefaultBuildPipeline: config.defaultPipeline,
			ContainerImage:       image,
			SkipOffboardingPr:    config.skipOffboardingPr,
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitURL:   gitUrl,
					Versions: versions,
				},
			},
		},
	}
}

// TODO remove after only new model is used
func getComponentDataOldModel(config componentConfigOldModel) *compapiv1alpha1.Component {
	name := config.componentKey.Name
	if name == "" {
		name = HASCompName
	}
	namespace := config.componentKey.Namespace
	if namespace == "" {
		namespace = HASAppNamespace
	}
	image := config.containerImage
	if image == "" {
		image = ComponentContainerImage
	}
	gitUrl := config.gitURL
	if gitUrl == "" {
		gitUrl = SampleRepoLink + "-" + name
	}
	gitRevision := config.gitRevision
	if gitRevision == "" {
		gitRevision = "main"
	}
	application := config.application
	if application == "" {
		application = HASAppName
	}
	annotations := make(map[string]string)
	if config.annotations != nil {
		for key, value := range config.annotations {
			annotations[key] = value
		}
	}
	finalizers := []string{}
	if len(config.finalizers) != 0 {
		finalizers = append(finalizers, config.finalizers...)
	}
	return &compapiv1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "appstudio.redhat.com/v1alpha1",
			Kind:       "Component",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
			Finalizers:  finalizers,
		},
		Spec: compapiv1alpha1.ComponentSpec{
			ComponentName:  name,
			Application:    application,
			ContainerImage: image,
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitSource: &compapiv1alpha1.GitSource{
						URL:      gitUrl,
						Revision: gitRevision,
						Context:  config.gitSourceContext,
					},
				},
			},
		},
	}
}

func getSampleComponentData(componentKey types.NamespacedName) *compapiv1alpha1.Component {
	return getComponentData(componentConfig{componentKey: componentKey})
}

// TODO remove after only new model is used
func getSampleComponentDataOldModel(componentKey types.NamespacedName) *compapiv1alpha1.Component {
	return getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey})
}

// TODO remove after only new model is used
// createComponentAndProcessBuildRequest create a component with specified build request and
// waits until the request annotation is removed, which means the request was processed by the operator.
// Use createCustomComponentWithBuildRequest if there is no need to wait.
func createComponentAndProcessBuildRequest(config componentConfigOldModel, buildRequest string) {
	createCustomComponentWithBuildRequest(config, buildRequest)
	waitComponentAnnotationGone(config.componentKey, BuildRequestAnnotationName)
}

// TODO remove after only new model is used
func createCustomComponentWithoutBuildRequest(config componentConfigOldModel) {
	component := getComponentDataOldModel(config)
	if component.Annotations == nil {
		component.Annotations = make(map[string]string)
	}

	Expect(k8sClient.Create(ctx, component)).Should(Succeed())
	getComponent(config.componentKey)
}

// TODO remove after only new model is used
func createCustomComponentWithBuildRequest(config componentConfigOldModel, buildRequest string) {
	component := getComponentDataOldModel(config)
	if component.Annotations == nil {
		component.Annotations = make(map[string]string)
	}
	component.Annotations[BuildRequestAnnotationName] = buildRequest

	Expect(k8sClient.Create(ctx, component)).Should(Succeed())
	getComponent(config.componentKey)
}

// TODO remove after only new model is used
func setComponentBuildRequestOldModel(componentKey types.NamespacedName, buildRequest string) {
	component := getComponent(componentKey)
	if component.Annotations == nil {
		component.Annotations = make(map[string]string)
	}
	component.Annotations[BuildRequestAnnotationName] = buildRequest

	Expect(k8sClient.Update(ctx, component)).To(Succeed())
}

// TODO remove after only new model is used
// createComponentOldModel creates sample component resource and verifies it was properly created
func createComponentOldModel(componentLookupKey types.NamespacedName) {
	component := getSampleComponentDataOldModel(componentLookupKey)

	createComponentCustomOldModel(component)
}

// createComponent creates custom component resource and verifies it was properly created
func createComponent(sampleComponentData *compapiv1alpha1.Component) *compapiv1alpha1.Component {
	Expect(k8sClient.Create(ctx, sampleComponentData)).Should(Succeed())

	lookupKey := types.NamespacedName{
		Name:      sampleComponentData.Name,
		Namespace: sampleComponentData.Namespace,
	}

	return getComponent(lookupKey)
}

// TODO remove after only new model is used
// createComponentCustomOldModel creates custom component resource and verifies it was properly created
func createComponentCustomOldModel(sampleComponentData *compapiv1alpha1.Component) {
	Expect(k8sClient.Create(ctx, sampleComponentData)).Should(Succeed())

	lookupKey := types.NamespacedName{
		Name:      sampleComponentData.Name,
		Namespace: sampleComponentData.Namespace,
	}

	getComponent(lookupKey)
}

func getComponent(componentKey types.NamespacedName) *compapiv1alpha1.Component {
	component := &compapiv1alpha1.Component{}
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, componentKey, component); err != nil {
			return false
		}
		return component.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return component
}

// deleteComponent deletes the specified component resource and verifies it was properly deleted
// deleteComponentAndOwnedResources deletes a component and all its owned resources
// that would be deleted by garbage collection in a real cluster.
// This includes: Repository, ServiceAccount, and incoming Secret.
func deleteComponentAndOwnedResources(componentKey types.NamespacedName) {
	// Get component to determine repository name before deletion
	component := &compapiv1alpha1.Component{}
	if err := k8sClient.Get(ctx, componentKey, component); err != nil {
		if !k8sErrors.IsNotFound(err) {
			Fail(err.Error())
		}
		// Component doesn't exist, nothing to clean up
		return
	}

	// Delete service account
	saKey := getComponentServiceAccountKey(componentKey)
	deleteServiceAccount(saKey)

	// Delete component
	deleteComponent(componentKey)

	// Generate repository name from component's git URL
	if component.Spec.Source.GitURL != "" {
		repositoryName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
		if err == nil {
			// Delete PaC Repository
			repositoryKey := types.NamespacedName{Namespace: componentKey.Namespace, Name: repositoryName}
			deletePaCRepository(repositoryKey)

			// Delete incoming secret
			incomingSecretName := getPaCIncomingSecretName(repositoryName)
			incomingSecretKey := types.NamespacedName{Namespace: componentKey.Namespace, Name: incomingSecretName}
			deleteSecret(incomingSecretKey)

			// Delete webhook secret (used for token-based auth)
			webhookSecretKey := types.NamespacedName{Namespace: componentKey.Namespace, Name: pipelinesAsCodeWebhooksSecretName}
			deleteSecret(webhookSecretKey)
		}
	}

	// Check if there are any remaining component service accounts in the namespace
	// If not, wait for RoleBinding to be deleted (it's only deleted when the last component is removed)
	saList := &corev1.ServiceAccountList{}
	listOpts := &client.ListOptions{Namespace: componentKey.Namespace}
	if err := k8sClient.List(ctx, saList, listOpts); err == nil {
		// Check if there are any service accounts with the component SA prefix
		// Component SA names are generated as "build-pipeline-" + componentName
		hasComponentSAs := false
		for _, sa := range saList.Items {
			if strings.HasPrefix(sa.Name, "build-pipeline-") {
				hasComponentSAs = true
				break
			}
		}
		// If no component service accounts remain, wait for RoleBinding to be deleted
		if !hasComponentSAs {
			buildRoleBindingKey := types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: componentKey.Namespace}
			waitRoleBindingGone(buildRoleBindingKey)
		}
	}
}

func deleteComponent(componentKey types.NamespacedName) {
	component := &compapiv1alpha1.Component{}

	// Check if the component exists
	if err := k8sClient.Get(ctx, componentKey, component); k8sErrors.IsNotFound(err) {
		return
	}

	// Delete
	Eventually(func() error {
		if err := k8sClient.Get(ctx, componentKey, component); err != nil {
			return err
		}
		return k8sClient.Delete(ctx, component)
	}, timeout, interval).Should(Succeed())

	// Wait for delete to finish
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, componentKey, component))
	}, timeout, interval).Should(BeTrue())
}

func waitFinalizerOnComponent(componentKey types.NamespacedName, finalizerName string, finalizerShouldBePresent bool) {
	component := &compapiv1alpha1.Component{}
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, componentKey, component); err != nil {
			return false
		}

		if finalizerShouldBePresent {
			return controllerutil.ContainsFinalizer(component, finalizerName)
		} else {
			return !controllerutil.ContainsFinalizer(component, finalizerName)
		}
	}, timeout, interval).Should(BeTrue())
}

func waitPaCFinalizerOnComponent(componentKey types.NamespacedName) {
	waitFinalizerOnComponent(componentKey, PaCProvisionFinalizer, true)
}

func waitPaCFinalizerOnComponentGone(componentKey types.NamespacedName) {
	waitFinalizerOnComponent(componentKey, PaCProvisionFinalizer, false)
}

// TODO remove after only new model is used
func waitDoneMessageOnComponentOldModel(componentKey types.NamespacedName) {
	Eventually(func() bool {
		buildStatus := readBuildStatus(getComponent(componentKey))
		return buildStatus.Message == "done"
	}, timeout, interval).Should(BeTrue())
}

// TODO remove after only new model is used
func expectPacBuildStatusOldModel(componentKey types.NamespacedName, state string, errID int, errMessage string, mergeURL string) {
	// in 1 test component is usually created (which triggers reconcile and adds message=done)
	// and then component is updated (and waits for message=done apart from other things)
	// we should wait for desired state as well, as there is small time window when
	// message might be still done from previous reconcile, but even though requested action already finished,
	// it didn't update status yet so it will get status from previous reconcile
	Eventually(func() bool {
		buildStatus := readBuildStatus(getComponent(componentKey))
		Expect(buildStatus).ToNot(BeNil())
		Expect(buildStatus.PaC).ToNot(BeNil())

		return buildStatus.PaC.State == state
	}, timeout, interval).Should(BeTrue())

	buildStatus := readBuildStatus(getComponent(componentKey))
	Expect(buildStatus).ToNot(BeNil())
	Expect(buildStatus.PaC).ToNot(BeNil())
	Expect(buildStatus.PaC.State).To(Equal(state))
	Expect(buildStatus.PaC.ErrId).To(Equal(errID))
	Expect(buildStatus.PaC.ErrMessage).To(Equal(errMessage))
	Expect(buildStatus.PaC.MergeUrl).To(Equal(mergeURL))
	if state == "enabled" {
		Expect(buildStatus.PaC.ConfigurationTime).ToNot(BeEmpty())
	} else {
		Expect(buildStatus.PaC.ConfigurationTime).To(BeEmpty())
	}
}

func listComponentPipelineRuns(componentKey types.NamespacedName) []tektonapi.PipelineRun {
	pipelineRuns := &tektonapi.PipelineRunList{}
	labelSelectors := client.ListOptions{
		Raw:       &metav1.ListOptions{LabelSelector: ComponentNameLabelName + "=" + componentKey.Name},
		Namespace: componentKey.Namespace,
	}
	err := k8sClient.List(ctx, pipelineRuns, &labelSelectors)
	Expect(err).ToNot(HaveOccurred())
	return pipelineRuns.Items
}

func deleteComponentPipelineRuns(componentKey types.NamespacedName) {
	for _, pipelineRun := range listComponentPipelineRuns(componentKey) {
		if err := k8sClient.Delete(ctx, &pipelineRun); err != nil {
			if !k8sErrors.IsNotFound(err) {
				Fail(err.Error())
			}
		}
	}
}

func createPaCPipelineRunWithName(resourceKey types.NamespacedName, pipelineRunName string) {
	pipelineRun := &tektonapi.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pipelineRunName,
			Namespace: resourceKey.Namespace,
			Labels: map[string]string{
				ComponentNameLabelName: resourceKey.Name,
			},
		},
	}
	Expect(k8sClient.Create(ctx, pipelineRun)).Should(Succeed())

	pipelineRunKey := types.NamespacedName{Namespace: pipelineRun.Namespace, Name: pipelineRun.Name}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, pipelineRunKey, pipelineRun)
		return err == nil
	}, timeout, interval).Should(BeTrue())
}

func ensureNoPipelineRunsCreated(componentLookupKey types.NamespacedName) {
	pipelineRuns := &tektonapi.PipelineRunList{}
	Consistently(func() bool {
		labelSelectors := client.ListOptions{
			Raw:       &metav1.ListOptions{LabelSelector: ComponentNameLabelName + "=" + componentLookupKey.Name},
			Namespace: componentLookupKey.Namespace,
		}
		Expect(k8sClient.List(ctx, pipelineRuns, &labelSelectors)).To(Succeed())
		return len(pipelineRuns.Items) == 0
	}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
}

func waitNoPipelineRunsForComponent(componentLookupKey types.NamespacedName) {
	pipelineRuns := &tektonapi.PipelineRunList{}
	Eventually(func() bool {
		labelSelectors := client.ListOptions{
			Raw:       &metav1.ListOptions{LabelSelector: ComponentNameLabelName + "=" + componentLookupKey.Name},
			Namespace: componentLookupKey.Namespace,
		}
		Expect(k8sClient.List(ctx, pipelineRuns, &labelSelectors)).To(Succeed())
		return len(pipelineRuns.Items) == 0
	}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
}

func createImageRepositoryForComponent(componentKey types.NamespacedName) *imgcv1alpha1.ImageRepository {
	component := getComponent(componentKey)

	imageRepository := &imgcv1alpha1.ImageRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      componentKey.Name,
			Namespace: componentKey.Namespace,
			Labels: map[string]string{
				"appstudio.redhat.com/application": component.Spec.Application,
				"appstudio.redhat.com/component":   component.Name,
			},
		},
		Spec: imgcv1alpha1.ImageRepositorySpec{
			Image: imgcv1alpha1.ImageParameters{
				Name:       component.Namespace + "/" + component.Name,
				Visibility: imgcv1alpha1.ImageVisibility(imgcv1alpha1.ImageVisibilityPublic),
			},
		},
	}
	Expect(controllerutil.SetOwnerReference(component, imageRepository, scheme.Scheme)).To(Succeed())
	Expect(k8sClient.Create(ctx, imageRepository)).To(Succeed())
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, componentKey, imageRepository); err != nil {
			return false
		}
		return imageRepository.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())

	Expect(k8sClient.Get(ctx, componentKey, imageRepository)).To(Succeed())
	imageRepository.Status = imgcv1alpha1.ImageRepositoryStatus{
		State: imgcv1alpha1.ImageRepositoryStateReady,
		Image: imgcv1alpha1.ImageStatus{
			URL:        "registry.io/test-org/" + imageRepository.Spec.Image.Name,
			Visibility: imageRepository.Spec.Image.Visibility,
		},
		Credentials: imgcv1alpha1.CredentialsStatus{
			PushSecretName: componentKey.Name + "-image-push",
			PullSecretName: componentKey.Name + "-image-pull",
		},
	}
	Expect(k8sClient.Status().Update(ctx, imageRepository))
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, componentKey, imageRepository); err != nil {
			return false
		}
		return imageRepository.Status.Image.URL != ""
	}, timeout, interval).Should(BeTrue())

	return imageRepository
}

func deleteImageRepository(resourceKey types.NamespacedName) {
	imageRepository := &imgcv1alpha1.ImageRepository{}
	if err := k8sClient.Get(ctx, resourceKey, imageRepository); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail(err.Error())
	}
	if err := k8sClient.Delete(ctx, imageRepository); err != nil {
		if !k8sErrors.IsNotFound(err) {
			Fail(err.Error())
		}
		return
	}
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, resourceKey, imageRepository))
	}, timeout, interval).Should(BeTrue())
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

	Eventually(func() error {
		secret := &corev1.Secret{}
		return k8sClient.Get(ctx, resourceKey, secret)
	}, timeout, interval).Should(Succeed())
}

func createSCMSecret(resourceKey types.NamespacedName, data map[string]string, secretType corev1.SecretType, annotations map[string]string, labels map[string]string) {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		Type: secretType,
		ObjectMeta: metav1.ObjectMeta{
			Name:        resourceKey.Name,
			Namespace:   resourceKey.Namespace,
			Labels:      labels,
			Annotations: annotations,
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

	Eventually(func() error {
		secret := &corev1.Secret{}
		return k8sClient.Get(ctx, resourceKey, secret)
	}, timeout, interval).Should(Succeed())
}

// TODO remove after only new model is used
func createSCMSecretOldModel(resourceKey types.NamespacedName, data map[string]string, secretType corev1.SecretType, annotations map[string]string) {
	labels := map[string]string{
		"appstudio.redhat.com/credentials": "scm",
		"appstudio.redhat.com/scm.host":    "github.com",
	}
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		Type: secretType,
		ObjectMeta: metav1.ObjectMeta{
			Name:        resourceKey.Name,
			Namespace:   resourceKey.Namespace,
			Labels:      labels,
			Annotations: annotations,
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

	Eventually(func() error {
		secret := &corev1.Secret{}
		return k8sClient.Get(ctx, resourceKey, secret)
	}, timeout, interval).Should(Succeed())
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

func waitSecretCreated(resourceKey types.NamespacedName) {
	secret := &corev1.Secret{}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, resourceKey, secret)
		return err == nil && secret.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
}

func waitSecretGone(resourceKey types.NamespacedName) {
	secret := &corev1.Secret{}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, resourceKey, secret)
		return k8sErrors.IsNotFound(err)
	}, timeout, interval).Should(BeTrue())
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

func deleteNamespace(name string) {
	namespace := corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	if err := k8sClient.Delete(ctx, &namespace); err != nil && !k8sErrors.IsNotFound(err) {
		Fail(err.Error())
	}
}

func waitPaCRepositoryCreated(resourceKey types.NamespacedName) *pacv1alpha1.Repository {
	pacRepository := &pacv1alpha1.Repository{}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, resourceKey, pacRepository)
		return err == nil && pacRepository.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return pacRepository
}

// validatePaCRepository validates Repository CR with expected URL and optional GitProvider config.
// When secretName is empty, it verifies GitProvider is nil (GitHub App authentication).
// When secretName is provided, it validates token-based authentication with the secret.
// The expected URL is determined using getGitRepoUrl, and GitProvider type using getGitProvider.
func validatePaCRepository(component *compapiv1alpha1.Component, secretName, secretKey string) *pacv1alpha1.Repository {
	// Generate repository name from git URL
	expectedRepoName, err := generatePaCRepositoryNameFromGitUrl(component.Spec.Source.GitURL)
	Expect(err).NotTo(HaveOccurred())
	resourceKey := types.NamespacedName{Namespace: component.Namespace, Name: expectedRepoName}

	repository := waitPaCRepositoryCreated(resourceKey)

	// Get expected URL using getGitRepoUrl
	expectedURL := getGitRepoUrl(*component, true)
	Expect(repository.Spec.URL).To(Equal(expectedURL))

	// Get git provider type
	gitProvider, err := getGitProvider(*component, true)
	Expect(err).NotTo(HaveOccurred())

	// Validate GitProvider
	if secretName == "" {
		// GitHub App - should have no GitProvider config
		Expect(repository.Spec.GitProvider).To(BeNil())
	} else {
		// Token auth - validate GitProvider config
		Expect(repository.Spec.GitProvider).NotTo(BeNil())

		// Determine expected GitProvider type (handle forgejo -> gitea mapping)
		expectedType := gitProvider
		if gitProvider == "forgejo" {
			expectedType = "gitea"
		}
		Expect(repository.Spec.GitProvider.Type).To(Equal(expectedType))

		// Determine expected GitProvider URL from source URL
		u, err := url.Parse(component.Spec.Source.GitURL)
		Expect(err).NotTo(HaveOccurred())
		var expectedGitProviderURL string
		if u.Host == "github.com" || u.Host == "gitlab.com" {
			expectedGitProviderURL = ""
		} else {
			expectedGitProviderURL = u.Scheme + "://" + u.Host
		}
		Expect(repository.Spec.GitProvider.URL).To(Equal(expectedGitProviderURL))

		// Validate Secret
		Expect(repository.Spec.GitProvider.Secret).NotTo(BeNil())
		Expect(repository.Spec.GitProvider.Secret.Name).To(Equal(secretName))
		Expect(repository.Spec.GitProvider.Secret.Key).To(Equal(secretKey))

		// Validate WebhookSecret
		Expect(repository.Spec.GitProvider.WebhookSecret).NotTo(BeNil())
		Expect(repository.Spec.GitProvider.WebhookSecret.Name).To(Equal(pipelinesAsCodeWebhooksSecretName))
	}

	return repository
}

// validatePaCRepositoryIncomings validates Repository CR Incomings configuration.
// Verifies that the repository has the expected incoming webhook secret and targets.
// This should be called after validatePaCRepository.
// The secret name is automatically determined using getPaCIncomingSecretName.
// The secret key is always pacIncomingSecretKey, and params are always ["source_url"].
func validatePaCRepositoryIncomings(repository *pacv1alpha1.Repository, targets []string) {
	// Validate Incomings
	Expect(repository.Spec.Incomings).NotTo(BeNil())
	Expect(len(*repository.Spec.Incomings)).To(Equal(1))

	// Get expected secret name from repository name
	expectedSecretName := getPaCIncomingSecretName(repository.Name)
	Expect((*repository.Spec.Incomings)[0].Secret.Name).To(Equal(expectedSecretName))
	Expect((*repository.Spec.Incomings)[0].Secret.Key).To(Equal(pacIncomingSecretKey))

	// Validate targets
	if len(targets) > 0 {
		Expect((*repository.Spec.Incomings)[0].Targets).To(ContainElements(targets))
	} else {
		Expect((*repository.Spec.Incomings)[0].Targets).To(BeEmpty())
	}

	// Validate params (always source_url)
	Expect(len((*repository.Spec.Incomings)[0].Params)).To(Equal(1))
	Expect((*repository.Spec.Incomings)[0].Params).To(Equal([]string{"source_url"}))
}

func deletePaCRepository(resourceKey types.NamespacedName) {
	pacRepository := &pacv1alpha1.Repository{}
	if err := k8sClient.Get(ctx, resourceKey, pacRepository); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail(err.Error())
	}
	if err := k8sClient.Delete(ctx, pacRepository); err != nil {
		if !k8sErrors.IsNotFound(err) {
			Fail(err.Error())
		}
		return
	}
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, resourceKey, pacRepository))
	}, timeout, interval).Should(BeTrue())
}

func deleteAllPaCRepositories(namesapce string) {
	opts := &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{
			Namespace: namesapce,
		},
	}
	Expect(k8sClient.DeleteAllOf(ctx, &pacv1alpha1.Repository{}, opts)).To(Succeed())
}

func waitComponentAnnotationGone(componentKey types.NamespacedName, annotationName string) {
	Eventually(func() bool {
		component := getComponent(componentKey)
		annotations := component.GetAnnotations()
		if annotations == nil {
			return true
		}
		_, exists := annotations[annotationName]
		return !exists
	}, timeout, interval).Should(BeTrue())
}

func waitComponentAnnotationExists(componentKey types.NamespacedName, annotationName string) {
	Eventually(func() bool {
		component := getComponent(componentKey)
		annotations := component.GetAnnotations()
		if annotations == nil {
			return false
		}
		_, exists := annotations[annotationName]
		return exists
	}, timeout, interval).Should(BeTrue())
}

func waitForComponentStatusVersions(componentKey types.NamespacedName, expectedCount int) *compapiv1alpha1.Component {
	var component *compapiv1alpha1.Component
	Eventually(func() bool {
		component = getComponent(componentKey)
		return len(component.Status.Versions) == expectedCount
	}, timeout, interval).Should(BeTrue())
	return component
}

func waitForComponentStatusMessage(componentKey types.NamespacedName, shouldBeEmpty bool) *compapiv1alpha1.Component {
	var component *compapiv1alpha1.Component
	Eventually(func() bool {
		component = getComponent(componentKey)
		if shouldBeEmpty {
			return component.Status.Message == ""
		}
		return component.Status.Message != ""
	}, timeout, interval).Should(BeTrue())
	return component
}

func waitForComponentSpecActionEmpty(componentKey types.NamespacedName, actionType string, checkType string) *compapiv1alpha1.Component {
	var component *compapiv1alpha1.Component
	Eventually(func() bool {
		component = getComponent(componentKey)
		if actionType == "create-pr" {
			switch checkType {
			case "allversions":
				return !component.Spec.Actions.CreateConfiguration.AllVersions
			case "versions":
				return len(component.Spec.Actions.CreateConfiguration.Versions) == 0
			case "version":
				return component.Spec.Actions.CreateConfiguration.Version == ""
			}
		} else if actionType == "trigger" {
			switch checkType {
			case "builds":
				return len(component.Spec.Actions.TriggerBuilds) == 0
			case "build":
				return component.Spec.Actions.TriggerBuild == ""
			}
		}
		return false
	}, timeout, interval).Should(BeTrue())
	return component
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

func createDefaultBuildPipelineConfigMap(configMapKey types.NamespacedName) {
	createBuildPipelineConfigMap(configMapKey, defaultPipelineBundle, defaultPipelineName)
}

func createBuildPipelineConfigMap(configMapKey types.NamespacedName, pipelineBundle, pipelineName string) {
	configMapData := map[string]string{}
	buildPipelineData := pipelineConfig{
		DefaultPipelineName: pipelineName,
		Pipelines:           []BuildPipeline{{Name: pipelineName, Bundle: pipelineBundle, AdditionalParams: []string{"additional_param1"}}},
	}
	yamlData, _ := yaml.Marshal(&buildPipelineData)
	configMapData[buildPipelineConfigName] = string(yamlData)

	buildPipelineConfigMap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: configMapKey.Name, Namespace: configMapKey.Namespace},
		Data:       configMapData,
	}

	if err := k8sClient.Create(ctx, &buildPipelineConfigMap); err != nil && !k8sErrors.IsAlreadyExists(err) {
		Fail(err.Error())
	}
}

func createCAConfigMap(configMapKey types.NamespacedName) {
	configMapData := map[string]string{CaConfigMapKey: "someCAcertdata"}

	caConfigMap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: configMapKey.Name, Namespace: configMapKey.Namespace, Labels: map[string]string{CaConfigMapLabel: "true"}},
		Data:       configMapData,
	}

	if err := k8sClient.Create(ctx, &caConfigMap); err != nil && !k8sErrors.IsAlreadyExists(err) {
		Fail(err.Error())
	}
}

func createCustomRenovateConfigMap(configMapKey types.NamespacedName, configMapData map[string]string) {
	customConfigMap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: configMapKey.Name, Namespace: configMapKey.Namespace},
		Data:       configMapData,
	}
	if err := k8sClient.Create(ctx, &customConfigMap); err != nil && !k8sErrors.IsAlreadyExists(err) {
		Fail(err.Error())
	}
}

func deleteConfigMap(configMapKey types.NamespacedName) {
	configMap := corev1.ConfigMap{}
	if err := k8sClient.Get(ctx, configMapKey, &configMap); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail(err.Error())
	}
	if err := k8sClient.Delete(ctx, &configMap); err != nil && !k8sErrors.IsNotFound(err) {
		Fail(err.Error())
	}
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, configMapKey, &configMap))
	}, timeout, interval).Should(BeTrue())
}

func waitServiceAccount(serviceAccountKey types.NamespacedName) corev1.ServiceAccount {
	serviceAccount := corev1.ServiceAccount{}
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, serviceAccountKey, &serviceAccount); err != nil {
			return false
		}
		return serviceAccount.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())

	return serviceAccount
}

func isServiceAccountHasSecretLinked(serviceAccountKey types.NamespacedName, secretName string, isPullSecret bool) bool {
	serviceAccount := corev1.ServiceAccount{}
	if err := k8sClient.Get(ctx, serviceAccountKey, &serviceAccount); err != nil {
		return false
	}
	if isPullSecret {
		for _, pullSecretReference := range serviceAccount.ImagePullSecrets {
			if pullSecretReference.Name == secretName {
				return true
			}
		}
	} else {
		for _, secretReference := range serviceAccount.Secrets {
			if secretReference.Name == secretName {
				return true
			}
		}
	}
	return false
}

func waitServiceAccountLinkedSecret(serviceAccountKey types.NamespacedName, secretName string, isPullSecret bool) {
	Eventually(func() bool {
		return isServiceAccountHasSecretLinked(serviceAccountKey, secretName, isPullSecret)
	}, timeout, interval).Should(BeTrue())
}

func waitServiceAccountLinkedSecretGone(serviceAccountKey types.NamespacedName, secretName string, isPullSecret bool) {
	Eventually(func() bool {
		return !isServiceAccountHasSecretLinked(serviceAccountKey, secretName, isPullSecret)
	}, timeout, interval).Should(BeTrue())
}

func deleteServiceAccount(serviceAccountKey types.NamespacedName) {
	serviceAccount := &corev1.ServiceAccount{}

	// Check if the SA exists
	if err := k8sClient.Get(ctx, serviceAccountKey, serviceAccount); k8sErrors.IsNotFound(err) {
		return
	}

	// Delete
	Eventually(func() error {
		if err := k8sClient.Get(ctx, serviceAccountKey, serviceAccount); err != nil {
			return err
		}
		return k8sClient.Delete(ctx, serviceAccount)
	}, timeout, interval).Should(Succeed())

	// Wait for the deletion to finish
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, serviceAccountKey, serviceAccount))
	}, timeout, interval).Should(BeTrue())
}

func getComponentServiceAccountKey(componentKey types.NamespacedName) types.NamespacedName {
	return types.NamespacedName{
		Name:      "build-pipeline-" + componentKey.Name,
		Namespace: componentKey.Namespace,
	}
}

func waitRoleBindingGone(roleBindingKey types.NamespacedName) {
	roleBinding := rbacv1.RoleBinding{}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, roleBindingKey, &roleBinding)
		return k8sErrors.IsNotFound(err)
	}, timeout, interval).Should(BeTrue())
}

func waitServiceAccountInRoleBinding(roleBindingKey types.NamespacedName, serviceAccountName string) rbacv1.RoleBinding {
	roleBinding := rbacv1.RoleBinding{}
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, roleBindingKey, &roleBinding); err != nil {
			return false
		}
		for _, subject := range roleBinding.Subjects {
			if subject.Kind == "ServiceAccount" && subject.Name == serviceAccountName {
				return true
			}
		}
		return false
	}, timeout, interval).Should(BeTrue())

	return roleBinding
}

func waitServiceAccountNotInRoleBinding(roleBindingKey types.NamespacedName, serviceAccountName string) rbacv1.RoleBinding {
	roleBinding := rbacv1.RoleBinding{}
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, roleBindingKey, &roleBinding); err != nil {
			return false
		}
		for _, subject := range roleBinding.Subjects {
			if subject.Kind == "ServiceAccount" && subject.Name == serviceAccountName {
				return false
			}
		}
		return true
	}, timeout, interval).Should(BeTrue())

	return roleBinding
}

func deleteRoleBinding(roleBindingKey types.NamespacedName) {
	roleBinding := rbacv1.RoleBinding{}
	if err := k8sClient.Get(ctx, roleBindingKey, &roleBinding); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail(err.Error())
	}
	if err := k8sClient.Delete(ctx, &roleBinding); err != nil && !k8sErrors.IsNotFound(err) {
		Fail(err.Error())
	}
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, roleBindingKey, &roleBinding))
	}, timeout, interval).Should(BeTrue())
}

func createReleasePlanAdmission(rpaKey types.NamespacedName, componentName string) {
	data, err := json.Marshal(map[string]interface{}{
		"mapping": map[string]interface{}{
			"components": []map[string]interface{}{
				{
					"name":       componentName,
					"repository": rpaMappingRepository1,
					"tags":       []string{"latest"},
					"repositories": []map[string]interface{}{
						{
							"url":  rpaMappingRepository2,
							"tags": []string{"latest"},
						},
						{
							"url":  rpaMappingRepository3,
							"tags": []string{"latest"},
						},
					},
				},
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())

	rpa := &releaseapi.ReleasePlanAdmission{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rpaKey.Name,
			Namespace: rpaKey.Namespace,
		},
		Spec: releaseapi.ReleasePlanAdmissionSpec{
			Applications: []string{"application"},
			Origin:       rpaKey.Namespace,
			Environment:  "environment",
			Policy:       "policy",
			Pipeline: &tektonutils.Pipeline{
				PipelineRef: tektonutils.PipelineRef{
					Resolver: "bundles",
					Params: []tektonutils.Param{
						{Name: "bundle", Value: "quay.io/some/bundle"},
						{Name: "name", Value: "release-pipeline"},
						{Name: "kind", Value: "pipeline"},
					},
				},
			},
			Data: &runtime.RawExtension{Raw: data},
		},
	}

	Expect(k8sClient.Create(ctx, rpa)).To(Succeed())
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, rpaKey, rpa); err != nil {
			return false
		}
		return rpa.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
}

// verifyPacWebhookIncomingPostData verifies PaC webhook incoming POST data against expected parameters
func verifyPacWebhookIncomingPostData(data map[string]interface{}, repository, secret, pipelinerun, namespace, branch, sourceUrl string) {
	Expect(data["repository"]).To(Equal(repository))
	Expect(data["secret"]).To(Equal(secret))
	Expect(data["pipelinerun"]).To(Equal(pipelinerun))
	Expect(data["namespace"]).To(Equal(namespace))
	Expect(data["branch"]).To(Equal(branch))
	Expect(data["params"].(map[string]interface{})["source_url"]).To(Equal(sourceUrl))
}

// verifyComponentVersionStatus verifies ComponentVersionStatus fields
// Pass empty string for configurationMergeURL to verify it's empty
// Pass "*" for configurationMergeURL to verify it's not empty
// Pass any other value for configurationMergeURL to verify exact match
// OnboardingTime is automatically verified: not empty when succeeded, empty when failed
// message: verifies exact Message field value (empty string = verify empty)
func verifyComponentVersionStatus(versionStatus compapiv1alpha1.ComponentVersionStatus, versionName, revision, onboardingStatus, configurationMergeURL, message string) {
	Expect(versionStatus.Name).To(Equal(versionName))
	Expect(versionStatus.Revision).To(Equal(revision))
	Expect(versionStatus.OnboardingStatus).To(Equal(onboardingStatus))

	// Verify OnboardingTime based on status
	if onboardingStatus == "succeeded" {
		Expect(versionStatus.OnboardingTime).NotTo(BeEmpty())
	} else {
		Expect(versionStatus.OnboardingTime).To(BeEmpty())
	}

	if configurationMergeURL == "*" {
		Expect(versionStatus.ConfigurationMergeURL).NotTo(BeEmpty())
	} else if configurationMergeURL != "" {
		Expect(versionStatus.ConfigurationMergeURL).To(Equal(configurationMergeURL))
	} else {
		Expect(versionStatus.ConfigurationMergeURL).To(BeEmpty())
	}

	Expect(versionStatus.Message).To(Equal(message))
}

// verifyMergeRequestData verifies MergeRequestData fields for PR creation
func verifyMergeRequestData(repoUrl string, mrData *gp.MergeRequestData, gitURL, commitMessage, title, branchName, baseBranchName string) {
	Expect(repoUrl).To(Equal(gitURL))
	Expect(mrData.CommitMessage).To(Equal(commitMessage))
	Expect(mrData.Title).To(Equal(title))
	Expect(mrData.BranchName).To(Equal(branchName))
	Expect(mrData.BaseBranchName).To(Equal(baseBranchName))
	Expect(mrData.Text).ToNot(BeEmpty())
	Expect(mrData.AuthorName).ToNot(BeEmpty())
	Expect(mrData.AuthorEmail).ToNot(BeEmpty())
}
