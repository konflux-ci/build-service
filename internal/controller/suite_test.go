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
	"context"
	"go/build"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	releaseapi "github.com/konflux-ci/release-service/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/konflux-ci/build-service/pkg/k8s"
	pacwebhook "github.com/konflux-ci/build-service/pkg/pacwebhook"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	log       logr.Logger
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

func getCacheExcludedObjectsTypes() []client.Object {
	return []client.Object{
		&corev1.Secret{},
		&corev1.ConfigMap{},
	}
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())
	log = ctrl.Log.WithName("testdebug")

	By("bootstrapping test environment")

	applicationApiDepVersion := "v0.0.0-20240812090716-e7eb2ecfb409"
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "hack", "routecrd", "route.yaml"),
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "github.com", "konflux-ci", "application-api@"+applicationApiDepVersion, "config", "crd", "bases"),
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "github.com", "tektoncd", "pipeline@v0.63.0", "config", "300-crds"),
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "github.com", "openshift-pipelines", "pipelines-as-code@v0.28.2", "config"),
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "github.com", "konflux-ci", "release-service@v0.0.0-20240610124538-758a1d48d002", "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	cfg.Timeout = 5 * time.Second

	err = routev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = appstudiov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = tektonapi.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = pacv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = releaseapi.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	defaultNS := &corev1.Namespace{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: HASAppNamespace}, defaultNS)).Should(Succeed())
	defaultNS.SetLabels(map[string]string{
		appstudioWorkspaceNameLabel: "build",
	})
	Expect(k8sClient.Update(ctx, defaultNS)).Should(Succeed())

	svcAccount := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      buildPipelineServiceAccountName,
			Namespace: "default",
		},
	}
	Expect(k8sClient.Create(context.Background(), &svcAccount)).Should(Succeed())

	clientOpts := client.Options{
		Cache: &client.CacheOptions{
			DisableFor: getCacheExcludedObjectsTypes(),
		},
	}

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Client: clientOpts,
	})
	Expect(err).ToNot(HaveOccurred())

	webhookConfig, err := pacwebhook.LoadMappingFromFile("", os.ReadFile)
	Expect(err).ToNot(HaveOccurred())

	err = (&ComponentBuildReconciler{
		Client:             k8sManager.GetClient(),
		Scheme:             k8sManager.GetScheme(),
		EventRecorder:      k8sManager.GetEventRecorderFor("ComponentOnboarding"),
		WebhookURLLoader:   pacwebhook.NewConfigWebhookURLLoader(webhookConfig),
		CredentialProvider: k8s.NewGitCredentialProvider(k8sManager.GetClient()),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&PaCPipelineRunPrunerReconciler{
		Client:        k8sManager.GetClient(),
		Scheme:        k8sManager.GetScheme(),
		EventRecorder: k8sManager.GetEventRecorderFor("PaCPipelineRunPruner"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ComponentDependencyUpdateReconciler{
		Client:                       k8sManager.GetClient(),
		ApiReader:                    k8sManager.GetAPIReader(),
		Scheme:                       k8sManager.GetScheme(),
		EventRecorder:                k8sManager.GetEventRecorderFor("ComponentDependencyUpdateReconciler"),
		ComponentDependenciesUpdater: *NewComponentDependenciesUpdater(k8sManager.GetClient(), k8sManager.GetScheme(), k8sManager.GetEventRecorderFor("ComponentDependencyUpdateReconciler")),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// TODO delete the controller after migration to the new dedicated to build Service Account.
	err = (&AppstudioPipelineServiceAccountWatcherReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
