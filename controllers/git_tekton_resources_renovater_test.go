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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	. "github.com/redhat-appstudio/build-service/pkg/common"
	"github.com/redhat-appstudio/build-service/pkg/git/github"
	"github.com/redhat-appstudio/build-service/pkg/renovate"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Git tekton resources renovater", func() {

	var (
		// All related to the component resources have the same key (but different type)
		pacSecretKey = types.NamespacedName{Name: gitopsprepare.PipelinesAsCodeSecretName, Namespace: BuildServiceNamespaceName}
	)

	Context("Test Renovate jobs creation", Label("renovater"), func() {

		_ = BeforeEach(func() {
			createNamespace(BuildServiceNamespaceName)
			pacSecretData := map[string]string{
				"github-application-id": "12345",
				"github-private-key":    githubAppPrivateKey,
			}
			createSecret(pacSecretKey, pacSecretData)
		})

		_ = AfterEach(func() {
			deleteBuildPipelineRunSelector(defaultSelectorKey)
			deleteJobs(BuildServiceNamespaceName)
			os.Unsetenv(renovate.InstallationsPerJobEnvName)
			deleteSecret(pacSecretKey)
		})

		It("It should trigger job", func() {
			installedRepositoryUrls := []string{
				"https://github/test/repo1",
				"https://github/test/repo2",
			}
			github.GetAllAppInstallations = func(appIdStr string, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				repositories := generateRepositories(installedRepositoryUrls)
				return []github.ApplicationInstallation{generateInstallation(repositories)}, "slug", nil
			}
			componentNamespacedName := createComponentForPaCBuild(getComponentData(componentConfig{componentKey: types.NamespacedName{Name: "testtrigger"}, gitURL: "https://github/test/repo1"}))
			createDefaultBuildPipelineRunSelector(defaultSelectorKey)
			Eventually(listJobs).WithArguments(BuildServiceNamespaceName).WithTimeout(timeout).Should(HaveLen(1))
			deleteComponent(componentNamespacedName)
		})

		It("It should trigger job, use default branch and default installation per job", func() {
			installedRepositoryUrls := []string{
				"https://github/test/repo1",
				"https://github/test/repo2",
			}
			github.GetAllAppInstallations = func(appIdStr string, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				repositories := generateRepositories(installedRepositoryUrls)
				return []github.ApplicationInstallation{generateInstallation(repositories)}, "slug", nil
			}
			componentsData := getComponentData(componentConfig{componentKey: types.NamespacedName{Name: "testdefault"}, gitURL: "https://github/test/repo1"})
			componentsData.Spec.Source.GitSource.Revision = ""
			componentNamespacedName := createComponentForPaCBuild(componentsData)
			os.Setenv(renovate.InstallationsPerJobEnvName, "0")
			createDefaultBuildPipelineRunSelector(defaultSelectorKey)
			Eventually(listJobs).WithArguments(BuildServiceNamespaceName).WithTimeout(timeout).Should(HaveLen(1))
			deleteComponent(componentNamespacedName)
		})

		It("It should trigger 2 jobs", func() {
			installedRepositories := make([][]string, 0, renovate.TasksPerJob*2)
			for i := 0; i < renovate.TasksPerJob*2; i++ {
				installedRepositories = append(installedRepositories, []string{
					fmt.Sprintf("https://github/test%v/repo%v", i, 1),
					fmt.Sprintf("https://github/test%v/repo%v", i, 2),
					fmt.Sprintf("https://github/test%v/repo%v", i, 3),
				})

			}
			var gitApplication []github.ApplicationInstallation
			for _, installedRepositoryUrls := range installedRepositories {
				gitApplication = append(gitApplication, generateInstallation(generateRepositories(installedRepositoryUrls)))
			}

			github.GetAllAppInstallations = func(appIdStr string, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				return gitApplication, "slug", nil
			}

			var componentsNs []types.NamespacedName
			for i, installedRepositoryUrls := range installedRepositories {
				for j, installedRepositoryUrl := range installedRepositoryUrls {
					componentsNs = append(componentsNs, createComponentForPaCBuild(getComponentData(componentConfig{componentKey: types.NamespacedName{Name: fmt.Sprintf("test%v-%v", i, j)}, gitURL: installedRepositoryUrl})))
				}
			}
			createDefaultBuildPipelineRunSelector(defaultSelectorKey)
			Eventually(listJobs).WithArguments(BuildServiceNamespaceName).WithTimeout(timeout).Should(HaveLen(2))
			//now delete all components
			for _, componentNs := range componentsNs {
				deleteComponent(componentNs)
			}
		})

		It("It should trigger job, because pac secret is missing", func() {
			installedRepositoryUrls := []string{
				"https://github/test/repo1",
				"https://github/test/repo2",
			}
			github.GetAllAppInstallations = func(appIdStr string, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				repositories := generateRepositories(installedRepositoryUrls)
				return []github.ApplicationInstallation{generateInstallation(repositories)}, "slug", nil
			}
			componentNamespacedName := createComponentForPaCBuild(getComponentData(componentConfig{componentKey: types.NamespacedName{Name: "testpacmissing"}, gitURL: "https://github/test/repo1"}))

			deleteSecret(pacSecretKey)
			createDefaultBuildPipelineRunSelector(defaultSelectorKey)
			Eventually(listEvents).WithArguments("default").WithTimeout(timeout).ShouldNot(HaveLen(0))
			allEvents := listEvents("default")
			Expect(allEvents[0].Reason).To(Equal("ErrorReadingPaCSecret"))
			deleteComponent(componentNamespacedName)
		})

		It("It should not trigger job", func() {
			installedRepositoryUrls := []string{
				"https://github/test/repo1",
				"https://github/test/repo2",
			}
			github.GetAllAppInstallations = func(appIdStr string, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				repositories := generateRepositories(installedRepositoryUrls)
				return []github.ApplicationInstallation{generateInstallation(repositories)}, "slug", nil
			}
			componentNamespacedName := createComponentForPaCBuild(getComponentData(componentConfig{componentKey: types.NamespacedName{Name: "testnottrigger"}, gitURL: "https://github/test/repo3"}))
			createDefaultBuildPipelineRunSelector(defaultSelectorKey)
			Consistently(listJobs).WithArguments(BuildServiceNamespaceName).WithTimeout(time.Second * 5).Should(BeEmpty())

			deleteComponent(componentNamespacedName)
		})
	})
})
