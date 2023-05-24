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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	"github.com/redhat-appstudio/build-service/pkg/git/github"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Git tekton resources renovater", func() {

	var (
		// All related to the component resources have the same key (but different type)
		pacSecretKey    = types.NamespacedName{Name: gitopsprepare.PipelinesAsCodeSecretName, Namespace: buildServiceNamespaceName}
		defaultSelector = types.NamespacedName{Name: buildPipelineSelectorResourceName, Namespace: buildServiceNamespaceName}
	)

	Context("Test Renovate jobs creation", Label("renovater"), func() {

		_ = BeforeEach(func() {
			createNamespace(buildServiceNamespaceName)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
		})

		_ = AfterEach(func() {
			deleteBuildPipelineRunSelector(defaultSelector)
			deleteJobs(buildServiceNamespaceName)
			os.Unsetenv(InstallationsPerJobEnvName)
		})

		It("It should not trigger job", func() {
			installedRepositoryUrls := []string{
				"https://github/test/repo1",
				"https://github/test/repo2",
			}
			github.GetAppInstallations = func(appIdStr string, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				repositories := generateRepositories(installedRepositoryUrls)
				return []github.ApplicationInstallation{generateInstallation(repositories)}, "slug", nil
			}
			componentNamespacedName := createComponentForPaCBuild(getComponentData(componentConfig{gitURL: "https://github/test/repo3"}))
			createBuildPipelineRunSelector(defaultSelector)
			time.Sleep(time.Second)
			Expect(listJobs(buildServiceNamespaceName)).Should(BeEmpty())
			deleteComponent(componentNamespacedName)
		})
		It("It should trigger job", func() {
			installedRepositoryUrls := []string{
				"https://github/test/repo1",
				"https://github/test/repo2",
			}
			github.GetAppInstallations = func(appIdStr string, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				repositories := generateRepositories(installedRepositoryUrls)
				return []github.ApplicationInstallation{generateInstallation(repositories)}, "slug", nil
			}
			componentNamespacedName := createComponentForPaCBuild(getComponentData(componentConfig{gitURL: "https://github/test/repo1"}))
			createBuildPipelineRunSelector(defaultSelector)
			time.Sleep(time.Second)
			Expect(listJobs(buildServiceNamespaceName)).Should(HaveLen(1))
			deleteComponent(componentNamespacedName)
		})
		It("It should trigger 2 jobs", func() {
			installedRepositoryUrls1 := []string{
				"https://github/test1/repo1",
				"https://github/test1/repo2",
			}
			installedRepositoryUrls2 := []string{
				"https://github/test2/repo1",
				"https://github/test2/repo2",
			}
			installedRepositoryUrls3 := []string{
				"https://github/test3/repo1",
				"https://github/test3/repo2",
			}
			installedRepositoryUrls4 := []string{
				"https://github/test4/repo1",
				"https://github/test4/repo2",
			}
			installedRepositoryUrls5 := []string{
				"https://github/test5/repo1",
				"https://github/test5/repo2",
			}
			github.GetAppInstallations = func(appIdStr string, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				return []github.ApplicationInstallation{
					generateInstallation(generateRepositories(installedRepositoryUrls1)),
					generateInstallation(generateRepositories(installedRepositoryUrls2)),
					generateInstallation(generateRepositories(installedRepositoryUrls3)),
					generateInstallation(generateRepositories(installedRepositoryUrls4)),
					generateInstallation(generateRepositories(installedRepositoryUrls5)),
				}, "slug", nil
			}

			// This is one installation - two matching repos
			componentNamespacedName1 := createComponentForPaCBuild(getComponentData(componentConfig{componentKey: types.NamespacedName{Name: "test1"}, gitURL: "https://github/test1/repo1"}))
			componentNamespacedName2 := createComponentForPaCBuild(getComponentData(componentConfig{componentKey: types.NamespacedName{Name: "test2"}, gitURL: "https://github/test1/repo2"}))
			// Second installation
			componentNamespacedName3 := createComponentForPaCBuild(getComponentData(componentConfig{componentKey: types.NamespacedName{Name: "test3"}, gitURL: "https://github/test2/repo1"}))
			// Third installation
			componentNamespacedName4 := createComponentForPaCBuild(getComponentData(componentConfig{componentKey: types.NamespacedName{Name: "test4"}, gitURL: "https://github/test3/repo2"}))
			// Set 2 installations per job
			os.Setenv(InstallationsPerJobEnvName, "2")
			createBuildPipelineRunSelector(defaultSelector)
			time.Sleep(time.Second)
			Expect(listJobs(buildServiceNamespaceName)).Should(HaveLen(2))
			deleteComponent(componentNamespacedName1)
			deleteComponent(componentNamespacedName2)
			deleteComponent(componentNamespacedName3)
			deleteComponent(componentNamespacedName4)
		})
	})
})
