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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	"github.com/redhat-appstudio/build-service/pkg/github"
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
		})

		It("It should not trigger job", func() {
			github.GetInstallations = func(appId int64, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				return generateInstallations(0), "slug", nil
			}
			createBuildPipelineRunSelector(defaultSelector)
			time.Sleep(time.Second)
			Expect(listJobs(buildServiceNamespaceName)).Should(BeEmpty())
		})
		It("It should trigger job", func() {
			github.GetInstallations = func(appId int64, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				return generateInstallations(1), "slug", nil
			}
			createBuildPipelineRunSelector(defaultSelector)
			time.Sleep(time.Second)
			Expect(listJobs(buildServiceNamespaceName)).Should(HaveLen(1))
		})
		It("It should trigger 3 jobs", func() {
			github.GetInstallations = func(appId int64, privateKeyPem []byte) ([]github.ApplicationInstallation, string, error) {
				return generateInstallations(InstallationsPerJob*2 + 1), "slug", nil
			}
			createBuildPipelineRunSelector(defaultSelector)
			time.Sleep(time.Second)
			Expect(listJobs(buildServiceNamespaceName)).Should(HaveLen(3))
		})
	})
})
