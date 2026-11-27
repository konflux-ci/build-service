/*
Copyright 2021-2026 Red Hat, Inc.

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

// TODO remove whole file after only new model is used and old model is gone
package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	compv1alpha1 "github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	"github.com/konflux-ci/build-service/pkg/boerrors"
	. "github.com/konflux-ci/build-service/pkg/common"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
	imgcv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
)

// fakeTektonDirectoryFiles wires the fake git provider client's GetFilesFromDirectoryFunc and
// DownloadFileContentFunc so the .tekton directory of the repository appears to contain exactly
// filesByPath (full path -> file content), regardless of the requested repoUrl/branchName.
func fakeTektonDirectoryFiles(filesByPath map[string][]byte) {
	GetFilesFromDirectoryFunc = func(repoUrl, branchName, directoryPath string) ([]gp.RepositoryFile, error) {
		files := make([]gp.RepositoryFile, 0, len(filesByPath))
		for path := range filesByPath {
			files = append(files, gp.RepositoryFile{FullPath: path})
		}
		return files, nil
	}
	DownloadFileContentFunc = func(repoUrl, branchName, filePath string) ([]byte, error) {
		content, ok := filesByPath[filePath]
		if !ok {
			return nil, fmt.Errorf("unexpected file requested: %s", filePath)
		}
		return content, nil
	}
}

// deleteApplicationOldModel deletes the old-model Application resource, if present, and waits for it
// to be gone.
func deleteApplicationOldModel(applicationKey types.NamespacedName) {
	application := &compapiv1alpha1.Application{}
	if err := k8sClient.Get(ctx, applicationKey, application); k8sErrors.IsNotFound(err) {
		return
	}

	Eventually(func() error {
		if err := k8sClient.Get(ctx, applicationKey, application); err != nil {
			return err
		}
		return k8sClient.Delete(ctx, application)
	}, timeout, interval).Should(Succeed())

	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, applicationKey, application))
	}, timeout, interval).Should(BeTrue())
}

// findRepositoryFile returns the entry in files with the given FullPath, or nil if not present.
func findRepositoryFile(files []gp.RepositoryFile, path string) *gp.RepositoryFile {
	for i := range files {
		if files[i].FullPath == path {
			return &files[i]
		}
	}
	return nil
}

// expectMigrationAnnotations asserts the exact current state of the Component's migration
// annotations: performMigrationAnnotationName must be present with value "true" iff
// expectPerformMigration, and migrationMapAnnotationName must unmarshal to expectedMigrationMap
// (and be absent entirely when expectedMigrationMap is nil).
func expectMigrationAnnotations(componentKey types.NamespacedName, expectPerformMigration bool, expectedMigrationMap map[string]string) {
	annotations := getComponent(componentKey).Annotations

	if expectPerformMigration {
		Expect(annotations).To(HaveKeyWithValue(performMigrationAnnotationName, "true"))
	} else {
		Expect(annotations).ToNot(HaveKey(performMigrationAnnotationName))
	}

	if expectedMigrationMap == nil {
		Expect(annotations).ToNot(HaveKey(migrationMapAnnotationName))
		return
	}
	var currentMigrationMap map[string]string
	Expect(json.Unmarshal([]byte(annotations[migrationMapAnnotationName]), &currentMigrationMap)).To(Succeed())
	Expect(currentMigrationMap).To(Equal(expectedMigrationMap))
}

// verifyMigrationMergeRequestFiles asserts migrationMRData matches the expected result of migrating a
// single old component's conventional (never renamed by the user) push/pull-request pipeline run files into
// newComponentName's versionName/revision: branch/base-branch names, exactly 4 file changes (delete the old
// push+PR files at their conventional old-model path, add the new push+PR files at the conventional
// new-model path), and that the added files are oldPushPR/oldPRPipelineRun correctly transformed per
// verifyMigratedPipelineRun below. expectedContext is the version's Context, passed through to
// verifyMigratedPipelineRun ("" if the version has none). Returns the parsed new push/PR PipelineRuns so
// the caller can run any additional assertions of its own.
func verifyMigrationMergeRequestFiles(migrationMRData *gp.MergeRequestData, oldComponentName, newComponentName, versionName, revision, expectedContext string, oldPushPR, oldPRPipelineRun *tektonapi.PipelineRun) (newPushPR, newPRPipelineRun tektonapi.PipelineRun) {
	Expect(migrationMRData).NotTo(BeNil())
	Expect(migrationMRData.BranchName).To(Equal(generateMigrationMergeRequestSourceBranch(newComponentName, revision)))
	Expect(migrationMRData.BaseBranchName).To(Equal(revision))
	Expect(migrationMRData.Files).To(HaveLen(4))

	oldPushPath := getPipelineRunDefinitionFilePathOldModel(oldComponentName, false)
	oldPRPath := getPipelineRunDefinitionFilePathOldModel(oldComponentName, true)
	newPushPath := getPipelineRunDefinitionFilePath(newComponentName, versionName, false)
	newPRPath := getPipelineRunDefinitionFilePath(newComponentName, versionName, true)

	deletedOldPush := findRepositoryFile(migrationMRData.Files, oldPushPath)
	Expect(deletedOldPush).NotTo(BeNil())
	Expect(deletedOldPush.Delete).To(BeTrue())
	deletedOldPR := findRepositoryFile(migrationMRData.Files, oldPRPath)
	Expect(deletedOldPR).NotTo(BeNil())
	Expect(deletedOldPR.Delete).To(BeTrue())

	addedNewPush := findRepositoryFile(migrationMRData.Files, newPushPath)
	Expect(addedNewPush).NotTo(BeNil())
	Expect(addedNewPush.Delete).To(BeFalse())
	addedNewPR := findRepositoryFile(migrationMRData.Files, newPRPath)
	Expect(addedNewPR).NotTo(BeNil())
	Expect(addedNewPR.Delete).To(BeFalse())

	Expect(yaml.Unmarshal(addedNewPush.Content, &newPushPR)).To(Succeed())
	Expect(yaml.Unmarshal(addedNewPR.Content, &newPRPipelineRun)).To(Succeed())

	verifyMigratedPipelineRun(oldPushPR, &newPushPR, oldComponentName, newComponentName, versionName, expectedContext, false, true)
	verifyMigratedPipelineRun(oldPRPipelineRun, &newPRPipelineRun, oldComponentName, newComponentName, versionName, expectedContext, true, true)

	return newPushPR, newPRPipelineRun
}

// verifyMigratedPipelineRun asserts that newPR is oldPR rewritten per the old-model -> new-model pipeline
// run migration table: name, labels, renamed annotations, the Context annotation (expectedContext, or its
// absence if "") and the build pipeline Service Account. isRename indicates whether the pipeline run file
// itself was renamed to its new-model path (the conventional-path case) rather than updated in place (the
// user-renamed-away-from-convention case) - only in the former case does the on-cel-expression path-change
// filter's reference to the pipeline's own filename get rewritten to the new one.
func verifyMigratedPipelineRun(oldPR, newPR *tektonapi.PipelineRun, oldComponentName, newComponentName, versionName, expectedContext string, isPullRequest, isRename bool) {
	Expect(newPR.ObjectMeta.Name).To(Equal(getPipelineRunDefinitionName(newComponentName, sanitizeVersionName(versionName), isPullRequest)))

	Expect(newPR.ObjectMeta.Labels).ToNot(HaveKey(ApplicationNameLabelName))
	Expect(newPR.ObjectMeta.Labels).ToNot(HaveKey(ComponentNameLabelNameOldModel))
	Expect(newPR.ObjectMeta.Labels[ComponentNameLabelName]).To(Equal(newComponentName))
	Expect(newPR.ObjectMeta.Labels).ToNot(HaveKey(PipelineRunTypeLabelNameOldModel))
	Expect(newPR.ObjectMeta.Labels[PipelineRunTypeLabelName]).To(Equal("build"))

	Expect(newPR.ObjectMeta.Annotations).ToNot(HaveKey(gitRepoAtShaAnnotationNameOldModel))
	Expect(newPR.ObjectMeta.Annotations[gitRepoAtShaAnnotationName]).To(Equal(oldPR.ObjectMeta.Annotations[gitRepoAtShaAnnotationNameOldModel]))
	Expect(newPR.ObjectMeta.Annotations).ToNot(HaveKey(gitCommitShaAnnotationNameOldModel))
	Expect(newPR.ObjectMeta.Annotations[gitCommitShaAnnotationName]).To(Equal(oldPR.ObjectMeta.Annotations[gitCommitShaAnnotationNameOldModel]))
	Expect(newPR.ObjectMeta.Annotations).ToNot(HaveKey(gitTargetBranchAnnotationNameOldModel))
	Expect(newPR.ObjectMeta.Annotations[gitTargetBranchAnnotationName]).To(Equal(oldPR.ObjectMeta.Annotations[gitTargetBranchAnnotationNameOldModel]))

	if isPullRequest {
		Expect(newPR.ObjectMeta.Annotations).ToNot(HaveKey(gitPRAnnotationNameOldModel))
		Expect(newPR.ObjectMeta.Annotations[gitPRAnnotationName]).To(Equal(oldPR.ObjectMeta.Annotations[gitPRAnnotationNameOldModel]))
	}

	if expectedContext != "" {
		Expect(newPR.ObjectMeta.Annotations[ContextAnnotationName]).To(Equal(expectedContext))
	} else {
		Expect(newPR.ObjectMeta.Annotations).ToNot(HaveKey(ContextAnnotationName))
	}

	Expect(newPR.ObjectMeta.Annotations[VersionAnnotationName]).To(Equal(versionName))

	// When the file itself was renamed to its new-model path, any on-cel-expression path-change filter
	// referencing its own old filename must follow it to the new one. Computed once and reused below in
	// the whole-object comparison, so the two checks can't drift apart.
	expectedCelExpression := oldPR.ObjectMeta.Annotations[pacCelExpressionAnnotationName]
	if isRename {
		oldFileName := getPipelineRunDefinitionFileNameOldModel(oldComponentName, isPullRequest)
		newFileName := getPipelineRunDefinitionFileName(newComponentName, sanitizeVersionName(versionName), isPullRequest)
		expectedCelExpression = strings.ReplaceAll(expectedCelExpression, ".tekton/"+oldFileName, ".tekton/"+newFileName)
	}
	expectedCelExpression = fmt.Sprintf(`(%s) && %s == "true"`, expectedCelExpression, BuildsEnabledParamName)
	Expect(newPR.ObjectMeta.Annotations[pacCelExpressionAnnotationName]).To(Equal(expectedCelExpression))

	Expect(newPR.Spec.TaskRunTemplate.ServiceAccountName).To(Equal(getBuildPipelineServiceAccountName(newComponentName)))

	// Everything above covers the documented transformation table. Build the exact expected result by
	// applying that same table (reusing renameAnnotation from transformPipelineRunForMigration itself) to a
	// copy of oldPR, then assert whole-object equality against newPR - this catches any other, undocumented
	// difference (Spec.PipelineRef, params, workspaces, other labels/annotations, Namespace, ...) that the
	// field-by-field checks above wouldn't notice.
	expectedPR := oldPR.DeepCopy()
	expectedPR.ObjectMeta.Name = newPR.ObjectMeta.Name
	delete(expectedPR.ObjectMeta.Labels, ApplicationNameLabelName)
	delete(expectedPR.ObjectMeta.Labels, ComponentNameLabelNameOldModel)
	expectedPR.ObjectMeta.Labels[ComponentNameLabelName] = newComponentName
	delete(expectedPR.ObjectMeta.Labels, PipelineRunTypeLabelNameOldModel)
	expectedPR.ObjectMeta.Labels[PipelineRunTypeLabelName] = "build"
	renameAnnotation(expectedPR.ObjectMeta.Annotations, gitRepoAtShaAnnotationNameOldModel, gitRepoAtShaAnnotationName)
	renameAnnotation(expectedPR.ObjectMeta.Annotations, gitCommitShaAnnotationNameOldModel, gitCommitShaAnnotationName)
	if isPullRequest {
		renameAnnotation(expectedPR.ObjectMeta.Annotations, gitPRAnnotationNameOldModel, gitPRAnnotationName)
	}
	renameAnnotation(expectedPR.ObjectMeta.Annotations, gitTargetBranchAnnotationNameOldModel, gitTargetBranchAnnotationName)
	if expectedContext != "" {
		expectedPR.ObjectMeta.Annotations[ContextAnnotationName] = expectedContext
	}
	expectedPR.ObjectMeta.Annotations[VersionAnnotationName] = versionName
	if _, ok := expectedPR.ObjectMeta.Annotations[pacCelExpressionAnnotationName]; ok {
		expectedPR.ObjectMeta.Annotations[pacCelExpressionAnnotationName] = expectedCelExpression
	}
	expectedPR.Spec.TaskRunTemplate.ServiceAccountName = getBuildPipelineServiceAccountName(newComponentName)

	Expect(newPR).To(Equal(expectedPR))
}

var _ = Describe("Pipeline runs migration from old model to new model", func() {

	const (
		pacHost = "pac-host"
	)

	var (
		pacRouteKey  = types.NamespacedName{Name: pipelinesAsCodeRouteName, Namespace: pipelinesAsCodeNamespaceOpenshift}
		pacSecretKey = types.NamespacedName{Name: PipelinesAsCodeGitHubAppSecretName, Namespace: BuildServiceNamespaceName}
	)

	BeforeEach(func() {
		createNamespace(BuildServiceNamespaceName)
		createNamespace(pipelinesAsCodeNamespaceOpenshift)
		createRoute(pacRouteKey, pacHost)
		pacSecretData := map[string]string{
			"github-application-id": "12345",
			"github-private-key":    githubAppPrivateKey,
		}
		createSecret(pacSecretKey, pacSecretData)
		createDefaultBuildPipelineConfigMap(defaultPipelineConfigMapKey)

		ResetTestGitProviderClient()
	})

	Context("old Component migrating into a new Component with the same name", func() {
		var (
			namespace = "prmigration-samename"
			compKey   = types.NamespacedName{Name: "comp1-app1", Namespace: namespace}
			gitURL    = SampleRepoLink + "-migration-samename"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(compKey)
			deleteComponentOldModel(compKey)
			deleteServiceAccount(getComponentServiceAccountKey(compKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("leaves the shared build pipeline Service Account's secrets untouched, since old and new Component resolve to the same Service Account name", func() {
			// Onboard the old-model Component and capture the real PaC pipeline run definitions it generates
			oldFilesByPath := map[string][]byte{}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				for _, file := range d.Files {
					oldFilesByPath[file.FullPath] = file.Content
				}
				return "https://githost.com/mr/old-9999", nil
			}

			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey:     compKey,
				gitURL:           gitURL,
				gitRevision:      "main",
				gitSourceContext: ".",
				application:      "testapp-samename",
				annotations:      defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			Expect(oldFilesByPath).To(HaveLen(2))
			oldPushPath := getPipelineRunDefinitionFilePathOldModel(compKey.Name, false)
			oldPRPath := getPipelineRunDefinitionFilePathOldModel(compKey.Name, true)

			var oldPushPR, oldPRPipelineRun tektonapi.PipelineRun
			Expect(yaml.Unmarshal(oldFilesByPath[oldPushPath], &oldPushPR)).To(Succeed())
			Expect(yaml.Unmarshal(oldFilesByPath[oldPRPath], &oldPRPipelineRun)).To(Succeed())

			oldComponent := getComponentOldModel(compKey)

			// Link a secret onto the build pipeline Service Account - which, since the name is shared, is
			// the very same Service Account the new Component will use too. Its eligibility for migration
			// linking doesn't matter here: linkMigratedSecretsToBuildPipelineServiceAccount short-circuits
			// on a same-named old component before it ever reads the old SA's secrets or evaluates
			// isSecretEligibleForMigrationLinking, so there's nothing to distinguish (no ImageRepository,
			// Opaque or common-secret-labeled variants needed, unlike the different-name case below).
			secretKey := types.NamespacedName{Name: compKey.Name + "-scm-secret", Namespace: namespace}
			createSCMSecret(secretKey, map[string]string{"password": "token"}, corev1.SecretTypeBasicAuth, nil, nil)

			saKey := getComponentServiceAccountKey(compKey)
			sa := waitServiceAccount(saKey)
			sa.Secrets = append(sa.Secrets, corev1.ObjectReference{Name: secretKey.Name})
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())
			secretCountBeforeMigration := len(sa.Secrets)

			// Create the new-model Component with migration_map set, but perform_migration not yet requested
			migrationMap := map[string]string{compKey.Name: "main"}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not create a PaC configuration PR for the new Component, since no CreateConfiguration action was requested")
				return "", nil
			}

			migrationCallCount := 0
			var migrationMRData *gp.MergeRequestData
			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				migrationCallCount++
				migrationMRData = d
				return "https://githost.com/mr/migrate-9999", nil
			}

			// .tekton can hold files unrelated to migration; listOldModelPipelineRunCandidates must skip
			// all four of these without treating them as migration candidates or erroring out.
			// Not YAML at all - skipped by extension before it's even downloaded.
			oldFilesByPath[".tekton/README.md"] = []byte("# not a pipeline run\n")
			// Valid YAML, but not shaped like a PipelineRun (a list, not an object) - fails to unmarshal.
			oldFilesByPath[".tekton/not-a-pipelinerun.yaml"] = []byte("- just\n- a\n- list\n")
			// A real PipelineRun, but without the old-model component label - not a migration candidate.
			unrelatedPR := &tektonapi.PipelineRun{
				TypeMeta:   metav1.TypeMeta{Kind: "PipelineRun", APIVersion: "tekton.dev/v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "unrelated-pipelinerun", Labels: map[string]string{ComponentNameLabelName: "something-else"}},
			}
			unrelatedPRContent, err := yaml.Marshal(unrelatedPR)
			Expect(err).NotTo(HaveOccurred())
			oldFilesByPath[".tekton/unrelated-pipelinerun.yaml"] = unrelatedPRContent
			// A Task carrying the old-model component label: yaml.Unmarshal into tektonapi.PipelineRun
			// would succeed anyway (ObjectMeta is shared, TaskSpec fields just don't overlap with
			// PipelineRunSpec's), so this must be rejected by its Kind, not by unmarshal failing or the
			// label being absent.
			oldModelTask := &tektonapi.Task{
				TypeMeta:   metav1.TypeMeta{Kind: "Task", APIVersion: "tekton.dev/v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "unrelated-task", Labels: map[string]string{ComponentNameLabelNameOldModel: compKey.Name}},
			}
			oldModelTaskContent, err := yaml.Marshal(oldModelTask)
			Expect(err).NotTo(HaveOccurred())
			oldFilesByPath[".tekton/unrelated-task.yaml"] = oldModelTaskContent

			fakeTektonDirectoryFiles(oldFilesByPath)

			newComponent := getComponentData(componentConfig{
				componentKey: compKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "main", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(compKey, 1)

			// Migration wasn't requested yet (perform_migration annotation absent), so migration_map must
			// still carry its original, untouched value.
			expectMigrationAnnotations(compKey, false, migrationMap)

			// Request the migration via the perform_migration annotation (an annotation-only update)
			newComponent = getComponent(compKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			Eventually(func() map[string]string {
				return getComponent(compKey).Annotations
			}, timeout, interval).ShouldNot(HaveKey(migrationMapAnnotationName))
			expectMigrationAnnotations(compKey, false, nil)

			Expect(migrationCallCount).To(Equal(1))

			// The pipeline run files were never renamed by the user, so the migration PR must rename and
			// transform them, same as in the different-name case.
			verifyMigrationMergeRequestFiles(migrationMRData, compKey.Name, compKey.Name, "main", "main", ".", &oldPushPR, &oldPRPipelineRun)

			// Secret linking is a no-op here: linkMigratedSecretsToBuildPipelineServiceAccount
			// (component_build_controller_service_account.go) explicitly skips an old component name that
			// equals the new Component's name, since they resolve to the exact same build pipeline Service
			// Account - there's nothing to copy, the secrets were never "someone else's" to begin with, so
			// the Service Account's Secrets list must be exactly what was put there above, no more, no less.
			sa = waitServiceAccount(saKey)
			Expect(sa.Secrets).To(HaveLen(secretCountBeforeMigration))
			Expect(isServiceAccountHasSecretLinked(saKey, secretKey.Name, false)).To(BeTrue())

			// Both the old and new Component now own the single shared Service Account.
			newComponentObj := getComponent(compKey)
			Expect(sa.OwnerReferences).To(HaveLen(2))
			Expect([]types.UID{sa.OwnerReferences[0].UID, sa.OwnerReferences[1].UID}).To(ConsistOf(oldComponent.UID, newComponentObj.UID))

			// The shared PaC Repository CR (keyed by git URL, same for both models) is likewise owned by
			// both Components.
			repository := waitPaCRepositoryCreated(repositoryNameKey)
			Expect(repository.OwnerReferences).To(HaveLen(2))
			Expect([]types.UID{repository.OwnerReferences[0].UID, repository.OwnerReferences[1].UID}).To(ConsistOf(oldComponent.UID, newComponentObj.UID))

			// Removing the old-model Component must not affect the shared Service Account or its secret.
			deleteComponentOldModel(compKey)

			sa = waitServiceAccount(saKey)
			Expect(sa.Secrets).To(HaveLen(secretCountBeforeMigration))
			Expect(isServiceAccountHasSecretLinked(saKey, secretKey.Name, false)).To(BeTrue())
		})
	})

	Context("single old Component migrating into one new Component version with a different name, testing secrets linking", func() {
		var (
			namespace       = "prmigration-single"
			oldCompKey      = types.NamespacedName{Name: "comp1-app1", Namespace: namespace}
			oldAppKey       = types.NamespacedName{Name: "testapp1", Namespace: namespace}
			oldImageRepoKey = types.NamespacedName{Name: "imagerepository-comp1-app1", Namespace: namespace}
			newCompKey      = types.NamespacedName{Name: "comp1-app1-new", Namespace: namespace}
			gitURL          = SampleRepoLink + "-migration-single"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(newCompKey)
			deleteComponentOldModel(oldCompKey)
			deleteApplicationOldModel(oldAppKey)
			deleteServiceAccount(getComponentServiceAccountKey(oldCompKey))
			deleteServiceAccount(getComponentServiceAccountKey(newCompKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
			deleteImageRepository(oldImageRepoKey)
		})

		It("migrates the old Component's push/pull-request pipeline runs, links eligible secrets and clears migration annotations", func() {
			// Realistic old-model stack: an Application the old Component belongs to, mirroring a user's
			// setup - kept around so an Application-owned (excluded) secret, below, has a real owner.
			application := &compapiv1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{Name: oldAppKey.Name, Namespace: oldAppKey.Namespace},
				Spec:       compapiv1alpha1.ApplicationSpec{DisplayName: oldAppKey.Name},
			}
			Expect(k8sClient.Create(ctx, application)).To(Succeed())

			// Onboard the old-model Component and capture the real PaC pipeline run definitions it generates
			oldFilesByPath := map[string][]byte{}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Expect(d.Files).To(HaveLen(2))
				for _, file := range d.Files {
					oldFilesByPath[file.FullPath] = file.Content
				}
				return "https://githost.com/mr/old-1111", nil
			}

			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey:     oldCompKey,
				gitURL:           gitURL,
				gitRevision:      "main",
				gitSourceContext: ".",
				application:      oldAppKey.Name,
				annotations:      defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			Expect(oldFilesByPath).To(HaveLen(2))
			oldPushPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, false)
			oldPRPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, true)
			Expect(oldFilesByPath).To(HaveKey(oldPushPath))
			Expect(oldFilesByPath).To(HaveKey(oldPRPath))

			var oldPushPR, oldPRPipelineRun tektonapi.PipelineRun
			Expect(yaml.Unmarshal(oldFilesByPath[oldPushPath], &oldPushPR)).To(Succeed())
			Expect(yaml.Unmarshal(oldFilesByPath[oldPRPath], &oldPRPipelineRun)).To(Succeed())

			// Link one eligible and one ImageRepository-owned (excluded) secret to the old build SA
			imageRepo := &imgcv1alpha1.ImageRepository{
				ObjectMeta: metav1.ObjectMeta{Name: oldImageRepoKey.Name, Namespace: oldImageRepoKey.Namespace},
				Spec: imgcv1alpha1.ImageRepositorySpec{
					Image: imgcv1alpha1.ImageParameters{Name: namespace + "/" + oldCompKey.Name, Visibility: imgcv1alpha1.ImageVisibilityPublic},
				},
			}
			Expect(k8sClient.Create(ctx, imageRepo)).To(Succeed())

			eligibleSecretKey := types.NamespacedName{Name: oldCompKey.Name + "-scm-secret", Namespace: namespace}
			createSCMSecret(eligibleSecretKey, map[string]string{"password": "token"}, corev1.SecretTypeBasicAuth, nil, map[string]string{"test": "eligible"})

			imagerepositoryOwnedSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: oldCompKey.Name + "-image-secret", Namespace: namespace},
				Type:       corev1.SecretTypeBasicAuth,
				StringData: map[string]string{"password": "token"},
			}
			Expect(controllerutil.SetOwnerReference(imageRepo, imagerepositoryOwnedSecret, scheme.Scheme)).To(Succeed())
			Expect(k8sClient.Create(ctx, imagerepositoryOwnedSecret)).To(Succeed())

			// Application-owned secret: excluded from migration linking by ownership, same reasoning as the
			// ImageRepository-owned one above but exercising the other half of isSecretEligibleForMigrationLinking's
			// ownership check.
			applicationOwnedSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: oldCompKey.Name + "-app-secret", Namespace: namespace},
				Type:       corev1.SecretTypeBasicAuth,
				StringData: map[string]string{"password": "token"},
			}
			Expect(controllerutil.SetOwnerReference(application, applicationOwnedSecret, scheme.Scheme)).To(Succeed())
			Expect(k8sClient.Create(ctx, applicationOwnedSecret)).To(Succeed())

			// Opaque secret: excluded from migration linking by type, same as the ImageRepository-owned one above.
			opaqueSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: oldCompKey.Name + "-opaque-secret", Namespace: namespace},
				Type:       corev1.SecretTypeOpaque,
				StringData: map[string]string{"token": "value"},
			}
			Expect(k8sClient.Create(ctx, opaqueSecret)).To(Succeed())

			// Common-secret-labeled secret: added to the old SA's Secrets below too, so migration linking
			// (which reads from the old SA's own Secrets list) actually encounters it as a candidate and has
			// to exclude it via isSecretEligibleForMigrationLinking's label check - not just end up linked
			// anyway via the separate, pre-existing common-secret auto-linking done when a build pipeline SA
			// is created/ensured (which matches by label across the namespace, independently of migration).
			commonLabeledSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      oldCompKey.Name + "-common-secret",
					Namespace: namespace,
					Labels:    map[string]string{commonBuildSecretLabelName: "true"},
				},
				Type:       corev1.SecretTypeBasicAuth,
				StringData: map[string]string{"password": "token"},
			}
			Expect(k8sClient.Create(ctx, commonLabeledSecret)).To(Succeed())

			// Namespace pull secret: excluded from migration linking by name, regardless of type, since it's
			// linked automatically to every build pipeline Service Account in the namespace.
			namespacePullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: namespacePullSecretName, Namespace: namespace},
				Type:       corev1.SecretTypeDockerConfigJson,
				StringData: map[string]string{".dockerconfigjson": "{}"},
			}
			Expect(k8sClient.Create(ctx, namespacePullSecret)).To(Succeed())

			oldSAKey := getComponentServiceAccountKey(oldCompKey)
			oldSA := waitServiceAccount(oldSAKey)
			oldSA.Secrets = append(oldSA.Secrets,
				corev1.ObjectReference{Name: eligibleSecretKey.Name},
				// Duplicate reference to the same eligible secret: migration linking must process it once.
				corev1.ObjectReference{Name: eligibleSecretKey.Name},
				corev1.ObjectReference{Name: imagerepositoryOwnedSecret.Name},
				corev1.ObjectReference{Name: applicationOwnedSecret.Name},
				corev1.ObjectReference{Name: opaqueSecret.Name},
				corev1.ObjectReference{Name: commonLabeledSecret.Name},
				corev1.ObjectReference{Name: namespacePullSecret.Name},
				// Reference to a secret that no longer exists: migration linking must skip it, not fail.
				corev1.ObjectReference{Name: oldCompKey.Name + "-nonexistent-secret"},
			)
			Expect(k8sClient.Update(ctx, &oldSA)).To(Succeed())

			// Create the new-model Component with migration_map set, but perform_migration not yet requested
			migrationMap := map[string]string{oldCompKey.Name: "main"}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not create a PaC configuration PR for the new Component, since no CreateConfiguration action was requested")
				return "", nil
			}

			migrationCallCount := 0
			var migrationMRData *gp.MergeRequestData
			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				migrationCallCount++
				migrationMRData = d
				return "https://githost.com/mr/migrate-1111", nil
			}
			fakeTektonDirectoryFiles(oldFilesByPath)

			newComponent := getComponentData(componentConfig{
				componentKey: newCompKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "main", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(newCompKey, 1)

			// Migration wasn't requested yet (perform_migration annotation absent), so migration_map must
			// still carry its original, untouched value.
			expectMigrationAnnotations(newCompKey, false, migrationMap)

			// The new Component's build pipeline SA already exists at this point (normal v2 onboarding,
			// independent of migration) and has already picked up the common-secret-labeled secret via the
			// auto-linking done at SA creation time - before migration ever runs. This confirms linking the
			// common secret has nothing to do with migration.
			newSAKey := getComponentServiceAccountKey(newCompKey)
			Expect(isServiceAccountHasSecretLinked(newSAKey, commonLabeledSecret.Name, false)).To(BeTrue())
			secretCountBeforeMigration := len(waitServiceAccount(newSAKey).Secrets)

			// Request the migration via the perform_migration annotation (an annotation-only update)
			newComponent = getComponent(newCompKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			// Migration annotations must be cleared once the migration map is fully resolved
			Eventually(func() map[string]string {
				return getComponent(newCompKey).Annotations
			}, timeout, interval).ShouldNot(HaveKey(migrationMapAnnotationName))
			expectMigrationAnnotations(newCompKey, false, nil)

			Expect(migrationCallCount).To(Equal(1))

			verifyMigrationMergeRequestFiles(migrationMRData, oldCompKey.Name, newCompKey.Name, "main", "main", ".", &oldPushPR, &oldPRPipelineRun)

			// Eligible secret gets linked to the new Component's build pipeline Service Account; the
			// ImageRepository-owned, Application-owned and Opaque ones don't, since migration linking
			// excludes all three
			waitServiceAccountLinkedSecret(newSAKey, eligibleSecretKey.Name, false)
			Expect(isServiceAccountHasSecretLinked(newSAKey, imagerepositoryOwnedSecret.Name, false)).To(BeFalse())
			Expect(isServiceAccountHasSecretLinked(newSAKey, applicationOwnedSecret.Name, false)).To(BeFalse())
			Expect(isServiceAccountHasSecretLinked(newSAKey, opaqueSecret.Name, false)).To(BeFalse())
			Expect(isServiceAccountHasSecretLinked(newSAKey, namespacePullSecret.Name, false)).To(BeFalse())
			Expect(isServiceAccountHasSecretLinked(newSAKey, oldCompKey.Name+"-nonexistent-secret", false)).To(BeFalse())

			// Common-secret-labeled secret still ends up linked to the new SA, but only via the
			// pre-existing common-secret auto-linking done at SA creation time, not via migration
			waitServiceAccountLinkedSecret(newSAKey, commonLabeledSecret.Name, false)

			// Exactly the eligible secret was added to the new SA on top of what was already there (the
			// common-labeled secret) - no more, no less - and, unlike the same-name case, this is a freshly
			// created Service Account owned solely by the new Component.
			newSA := waitServiceAccount(newSAKey)
			Expect(newSA.Secrets).To(HaveLen(secretCountBeforeMigration + 1))
			Expect(newSA.OwnerReferences).To(HaveLen(1))
			Expect(newSA.OwnerReferences[0].UID).To(Equal(getComponent(newCompKey).UID))

			// The shared PaC Repository CR (keyed by git URL, same for both models) is owned by both the
			// old and new Component.
			oldComponent := getComponentOldModel(oldCompKey)
			repository := waitPaCRepositoryCreated(repositoryNameKey)
			Expect(repository.OwnerReferences).To(HaveLen(2))
			Expect([]types.UID{repository.OwnerReferences[0].UID, repository.OwnerReferences[1].UID}).To(ConsistOf(oldComponent.UID, getComponent(newCompKey).UID))

			// Remove the entire old-model stack: Application, Component and its ImageRepository
			deleteComponentOldModel(oldCompKey)
			deleteApplicationOldModel(oldAppKey)
			deleteImageRepository(oldImageRepoKey)

			// The new Component's build pipeline Service Account must be entirely unaffected by that removal.
			newSA = waitServiceAccount(newSAKey)
			Expect(newSA.Secrets).To(HaveLen(secretCountBeforeMigration + 1))
			Expect(isServiceAccountHasSecretLinked(newSAKey, eligibleSecretKey.Name, false)).To(BeTrue())
			Expect(isServiceAccountHasSecretLinked(newSAKey, commonLabeledSecret.Name, false)).To(BeTrue())
		})
	})

	Context("old Component's pipeline run renamed during migration, with a non-root context", func() {
		var (
			namespace  = "prmigration-cel-path"
			oldCompKey = types.NamespacedName{Name: "comp1-cel-path", Namespace: namespace}
			newCompKey = types.NamespacedName{Name: "comp1-cel-path-new", Namespace: namespace}
			gitURL     = SampleRepoLink + "-migration-cel-path"
			srcContext = "frontend"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(newCompKey)
			deleteComponentOldModel(oldCompKey)
			deleteServiceAccount(getComponentServiceAccountKey(oldCompKey))
			deleteServiceAccount(getComponentServiceAccountKey(newCompKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("rewrites the on-cel-expression path-change filter to reference the renamed pipeline file", func() {
			oldFilesByPath := map[string][]byte{}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				for _, file := range d.Files {
					oldFilesByPath[file.FullPath] = file.Content
				}
				return "https://githost.com/mr/old-7777", nil
			}

			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey:     oldCompKey,
				gitURL:           gitURL,
				gitRevision:      "main",
				gitSourceContext: srcContext,
				application:      "testapp1",
				annotations:      defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			Expect(oldFilesByPath).To(HaveLen(2))

			oldPushPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, false)
			oldPRPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, true)
			oldPushFileName := getPipelineRunDefinitionFileNameOldModel(oldCompKey.Name, false)

			var oldPushPR, oldPRPipelineRun tektonapi.PipelineRun
			Expect(yaml.Unmarshal(oldFilesByPath[oldPushPath], &oldPushPR)).To(Succeed())
			Expect(yaml.Unmarshal(oldFilesByPath[oldPRPath], &oldPRPipelineRun)).To(Succeed())

			// Sanity check: the old pipeline run's CEL filter really does reference its own old-model
			// filename (only happens for a non-root context) - otherwise this test would prove nothing.
			Expect(oldPushPR.ObjectMeta.Annotations[pacCelExpressionAnnotationName]).To(ContainSubstring(".tekton/" + oldPushFileName))

			migrationMap := map[string]string{oldCompKey.Name: "main"}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not create a PaC configuration PR for the new Component, since no CreateConfiguration action was requested")
				return "", nil
			}

			var migrationMRData *gp.MergeRequestData
			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				migrationMRData = d
				return "https://githost.com/mr/migrate-7777", nil
			}
			fakeTektonDirectoryFiles(oldFilesByPath)

			// Context is copied verbatim per version from the old Component at conversion time, so the
			// new version keeps the same non-root context.
			newComponent := getComponentData(componentConfig{
				componentKey: newCompKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "main", Revision: "main", Context: srcContext, DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(newCompKey, 1)

			newComponent = getComponent(newCompKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			Eventually(func() map[string]string {
				return getComponent(newCompKey).Annotations
			}, timeout, interval).ShouldNot(HaveKey(migrationMapAnnotationName))

			// verifyMigrationMergeRequestFiles (via verifyMigratedPipelineRun) already asserts the
			// on-cel-expression path-change filter was rewritten to reference the renamed pipeline file,
			// as part of its whole-object comparison - no need to duplicate that check here.
			newPushPR, _ := verifyMigrationMergeRequestFiles(migrationMRData, oldCompKey.Name, newCompKey.Name, "main", "main", srcContext, &oldPushPR, &oldPRPipelineRun)

			newPushFileName := getPipelineRunDefinitionFileName(newCompKey.Name, "main", false)
			celExpression := newPushPR.ObjectMeta.Annotations[pacCelExpressionAnnotationName]
			Expect(celExpression).To(ContainSubstring(".tekton/" + newPushFileName))
			Expect(celExpression).NotTo(ContainSubstring(".tekton/" + oldPushFileName))
		})
	})

	Context("old Component's conventional path coincides exactly with the new-model path", func() {
		var (
			namespace  = "prmigration-samepath"
			oldCompKey = types.NamespacedName{Name: "foo-bar", Namespace: namespace}
			newCompKey = types.NamespacedName{Name: "foo", Namespace: namespace}
			gitURL     = SampleRepoLink + "-migration-samepath"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(newCompKey)
			deleteComponentOldModel(oldCompKey)
			deleteServiceAccount(getComponentServiceAccountKey(oldCompKey))
			deleteServiceAccount(getComponentServiceAccountKey(newCompKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("updates the file in place, since the old and new pipeline run file names are the same", func() {
			// Old Component "foo-bar" migrating into new Component "foo" version "bar": the new-model
			// path (newComponentName + "-" + versionName + "-push.yaml") and the old-model conventional
			// path (oldComponentName + "-push.yaml") are the same string here, simply because of how
			// both names are generated - this is the routine outcome whenever the conversion script
			// extracts a version by stripping it off the end of the old Component's own name, not a
			// rare coincidence.
			oldFilesByPath := map[string][]byte{}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				for _, file := range d.Files {
					oldFilesByPath[file.FullPath] = file.Content
				}
				return "https://githost.com/mr/old-8888", nil
			}

			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey:     oldCompKey,
				gitURL:           gitURL,
				gitRevision:      "main",
				gitSourceContext: ".",
				application:      "testapp1",
				annotations:      defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			Expect(oldFilesByPath).To(HaveLen(2))

			oldPushPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, false)
			oldPRPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, true)

			var oldPushPR, oldPRPipelineRun tektonapi.PipelineRun
			Expect(yaml.Unmarshal(oldFilesByPath[oldPushPath], &oldPushPR)).To(Succeed())
			Expect(yaml.Unmarshal(oldFilesByPath[oldPRPath], &oldPRPipelineRun)).To(Succeed())

			// Sanity check: the new-model path really does coincide exactly with the old-model
			// conventional path - otherwise this test would prove nothing.
			newPushPath := getPipelineRunDefinitionFilePath(newCompKey.Name, "bar", false)
			newPRPath := getPipelineRunDefinitionFilePath(newCompKey.Name, "bar", true)
			Expect(newPushPath).To(Equal(oldPushPath))
			Expect(newPRPath).To(Equal(oldPRPath))

			migrationMap := map[string]string{oldCompKey.Name: "bar"}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not create a PaC configuration PR for the new Component, since no CreateConfiguration action was requested")
				return "", nil
			}

			var migrationMRData *gp.MergeRequestData
			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				migrationMRData = d
				return "https://githost.com/mr/migrate-8888", nil
			}
			fakeTektonDirectoryFiles(oldFilesByPath)

			newComponent := getComponentData(componentConfig{
				componentKey: newCompKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "bar", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(newCompKey, 1)

			newComponent = getComponent(newCompKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			Eventually(func() map[string]string {
				return getComponent(newCompKey).Annotations
			}, timeout, interval).ShouldNot(HaveKey(migrationMapAnnotationName))

			Expect(migrationMRData).NotTo(BeNil())
			// Content update in place at the single coincidentally-shared path: exactly 2 entries
			// (push + pull-request), neither marked for deletion - never a delete+add pair for the
			// same path.
			Expect(migrationMRData.Files).To(HaveLen(2))
			for _, file := range migrationMRData.Files {
				Expect(file.Delete).To(BeFalse())
			}

			updatedPush := findRepositoryFile(migrationMRData.Files, newPushPath)
			Expect(updatedPush).NotTo(BeNil())
			updatedPR := findRepositoryFile(migrationMRData.Files, newPRPath)
			Expect(updatedPR).NotTo(BeNil())

			var newPushPR, newPRPipelineRun tektonapi.PipelineRun
			Expect(yaml.Unmarshal(updatedPush.Content, &newPushPR)).To(Succeed())
			Expect(yaml.Unmarshal(updatedPR.Content, &newPRPipelineRun)).To(Succeed())

			verifyMigratedPipelineRun(&oldPushPR, &newPushPR, oldCompKey.Name, newCompKey.Name, "bar", ".", false, false)
			verifyMigratedPipelineRun(&oldPRPipelineRun, &newPRPipelineRun, oldCompKey.Name, newCompKey.Name, "bar", ".", true, false)
		})
	})

	Context("several old Components sharing one revision migrating into one new Component", func() {
		var (
			namespace   = "prmigration-multi"
			oldComp1Key = types.NamespacedName{Name: "comp1-app1", Namespace: namespace}
			oldComp2Key = types.NamespacedName{Name: "comp2-app1", Namespace: namespace}
			oldAppKey   = types.NamespacedName{Name: "testapp1", Namespace: namespace}
			newCompKey  = types.NamespacedName{Name: "comp1-app1-comp2-app1", Namespace: namespace}
			gitURL      = SampleRepoLink + "-migration-multi"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(newCompKey)
			deleteComponentOldModel(oldComp1Key)
			deleteComponentOldModel(oldComp2Key)
			deleteServiceAccount(getComponentServiceAccountKey(oldComp1Key))
			deleteServiceAccount(getComponentServiceAccountKey(oldComp2Key))
			deleteServiceAccount(getComponentServiceAccountKey(newCompKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("combines both old Components' pipeline runs into a single migration merge request", func() {
			oldFilesByPath := map[string][]byte{}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				for _, file := range d.Files {
					oldFilesByPath[file.FullPath] = file.Content
				}
				return "https://githost.com/mr/old-" + repoUrl, nil
			}

			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey: oldComp1Key,
				gitURL:       gitURL,
				gitRevision:  "test",
				application:  oldAppKey.Name,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey: oldComp2Key,
				gitURL:       gitURL,
				gitRevision:  "test",
				application:  oldAppKey.Name,
				annotations:  defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			Expect(oldFilesByPath).To(HaveLen(4))

			migrationMap := map[string]string{
				oldComp1Key.Name: oldComp1Key.Name,
				oldComp2Key.Name: oldComp2Key.Name,
			}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not create a PaC configuration PR for the new Component, since no CreateConfiguration action was requested")
				return "", nil
			}

			migrationCallCount := 0
			var migrationMRData *gp.MergeRequestData
			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				migrationCallCount++
				migrationMRData = d
				return "https://githost.com/mr/migrate-2222", nil
			}
			fakeTektonDirectoryFiles(oldFilesByPath)

			newComponent := getComponentData(componentConfig{
				componentKey: newCompKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: oldComp1Key.Name, Revision: "test", DockerfilePath: "docker/Dockerfile"},
					{Name: oldComp2Key.Name, Revision: "test", DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(newCompKey, 2)

			// Migration wasn't requested yet (perform_migration annotation absent), so migration_map must
			// still carry its original, untouched value.
			expectMigrationAnnotations(newCompKey, false, migrationMap)

			// Request the migration via the perform_migration annotation (an annotation-only update)
			newComponent = getComponent(newCompKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			Eventually(func() map[string]string {
				return getComponent(newCompKey).Annotations
			}, timeout, interval).ShouldNot(HaveKey(migrationMapAnnotationName))
			expectMigrationAnnotations(newCompKey, false, nil)

			// Both old Components share one revision, so they're combined into a single merge request.
			Expect(migrationCallCount).To(Equal(1))

			Expect(migrationMRData).NotTo(BeNil())
			Expect(migrationMRData.BranchName).To(Equal(generateMigrationMergeRequestSourceBranch(newCompKey.Name, "test")))
			Expect(migrationMRData.BaseBranchName).To(Equal("test"))
			// 2 old Components x (push + pull-request) x (delete old path + add new path) = 8 file entries.
			Expect(migrationMRData.Files).To(HaveLen(8))

			for _, oldCompName := range []string{oldComp1Key.Name, oldComp2Key.Name} {
				for _, isPullRequest := range []bool{false, true} {
					oldPath := getPipelineRunDefinitionFilePathOldModel(oldCompName, isPullRequest)
					newPath := getPipelineRunDefinitionFilePath(newCompKey.Name, oldCompName, isPullRequest)

					var oldPR tektonapi.PipelineRun
					Expect(yaml.Unmarshal(oldFilesByPath[oldPath], &oldPR)).To(Succeed())

					deletedFile := findRepositoryFile(migrationMRData.Files, oldPath)
					Expect(deletedFile).NotTo(BeNil())
					Expect(deletedFile.Delete).To(BeTrue())

					addedFile := findRepositoryFile(migrationMRData.Files, newPath)
					Expect(addedFile).NotTo(BeNil())
					Expect(addedFile.Delete).To(BeFalse())

					var newPR tektonapi.PipelineRun
					Expect(yaml.Unmarshal(addedFile.Content, &newPR)).To(Succeed())

					// Context wasn't set on either version, so no context annotation should have been added.
					verifyMigratedPipelineRun(&oldPR, &newPR, oldCompName, newCompKey.Name, oldCompName, "", isPullRequest, true)
				}
			}

			// The shared PaC Repository CR (keyed by git URL, same for all three models) is owned by both
			// old Components and the new one.
			oldComp1 := getComponentOldModel(oldComp1Key)
			oldComp2 := getComponentOldModel(oldComp2Key)
			repository := waitPaCRepositoryCreated(repositoryNameKey)
			Expect(repository.OwnerReferences).To(HaveLen(3))
			Expect([]types.UID{repository.OwnerReferences[0].UID, repository.OwnerReferences[1].UID, repository.OwnerReferences[2].UID}).
				To(ConsistOf(oldComp1.UID, oldComp2.UID, getComponent(newCompKey).UID))
		})
	})

	Context("old Component's pipeline run files renamed by the user after the PaC configuration merge request was merged", func() {
		var (
			namespace  = "prmigration-renamed"
			oldCompKey = types.NamespacedName{Name: "comp1-app1-renamed", Namespace: namespace}
			oldAppKey  = types.NamespacedName{Name: "testapp1-renamed", Namespace: namespace}
			newCompKey = types.NamespacedName{Name: "comp1-app1-renamed-new", Namespace: namespace}
			gitURL     = SampleRepoLink + "-migration-renamed"

			renamedPushPath = ".tekton/push_pipeline_run.yaml"
			renamedPRPath   = ".tekton/pull_pipeline_run.yaml"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(newCompKey)
			deleteComponentOldModel(oldCompKey)
			deleteServiceAccount(getComponentServiceAccountKey(oldCompKey))
			deleteServiceAccount(getComponentServiceAccountKey(newCompKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("updates the renamed files' content in place instead of renaming them back", func() {
			// Onboard the old-model Component and capture the real PaC pipeline run definitions it generates
			oldFilesByPath := map[string][]byte{}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				for _, file := range d.Files {
					oldFilesByPath[file.FullPath] = file.Content
				}
				return "https://githost.com/mr/old-1111", nil
			}

			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey:     oldCompKey,
				gitURL:           gitURL,
				gitRevision:      "main",
				gitSourceContext: ".",
				application:      oldAppKey.Name,
				annotations:      defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			oldPushPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, false)
			oldPRPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, true)

			var oldPushPR, oldPRPipelineRun tektonapi.PipelineRun
			Expect(yaml.Unmarshal(oldFilesByPath[oldPushPath], &oldPushPR)).To(Succeed())
			Expect(yaml.Unmarshal(oldFilesByPath[oldPRPath], &oldPRPipelineRun)).To(Succeed())

			// Simulate the PaC configuration PR having been merged, and the user then renaming both
			// pipeline run files away from their conventional old-model paths (content is unchanged)
			renamedFilesByPath := map[string][]byte{
				renamedPushPath: oldFilesByPath[oldPushPath],
				renamedPRPath:   oldFilesByPath[oldPRPath],
			}

			migrationMap := map[string]string{oldCompKey.Name: "main"}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not create a PaC configuration PR for the new Component, since no CreateConfiguration action was requested")
				return "", nil
			}

			migrationCallCount := 0
			var migrationMRData *gp.MergeRequestData
			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				migrationCallCount++
				migrationMRData = d
				return "https://githost.com/mr/migrate-3333", nil
			}
			fakeTektonDirectoryFiles(renamedFilesByPath)

			newComponent := getComponentData(componentConfig{
				componentKey: newCompKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "main", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(newCompKey, 1)

			// Migration wasn't requested yet (perform_migration annotation absent), so migration_map must
			// still carry its original, untouched value.
			expectMigrationAnnotations(newCompKey, false, migrationMap)

			// Request the migration via the perform_migration annotation (an annotation-only update)
			newComponent = getComponent(newCompKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			Eventually(func() map[string]string {
				return getComponent(newCompKey).Annotations
			}, timeout, interval).ShouldNot(HaveKey(migrationMapAnnotationName))
			expectMigrationAnnotations(newCompKey, false, nil)

			Expect(migrationCallCount).To(Equal(1))

			Expect(migrationMRData).NotTo(BeNil())
			// Content updates in place for both renamed files: no delete+add rename pair, just 2 entries.
			Expect(migrationMRData.Files).To(HaveLen(2))
			for _, file := range migrationMRData.Files {
				Expect(file.Delete).To(BeFalse())
			}
			Expect(findRepositoryFile(migrationMRData.Files, getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, false))).To(BeNil())
			Expect(findRepositoryFile(migrationMRData.Files, getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, true))).To(BeNil())
			Expect(findRepositoryFile(migrationMRData.Files, getPipelineRunDefinitionFilePath(newCompKey.Name, "main", false))).To(BeNil())
			Expect(findRepositoryFile(migrationMRData.Files, getPipelineRunDefinitionFilePath(newCompKey.Name, "main", true))).To(BeNil())

			updatedPush := findRepositoryFile(migrationMRData.Files, renamedPushPath)
			Expect(updatedPush).NotTo(BeNil())
			updatedPR := findRepositoryFile(migrationMRData.Files, renamedPRPath)
			Expect(updatedPR).NotTo(BeNil())

			var newPushPR, newPRPipelineRun tektonapi.PipelineRun
			Expect(yaml.Unmarshal(updatedPush.Content, &newPushPR)).To(Succeed())
			Expect(yaml.Unmarshal(updatedPR.Content, &newPRPipelineRun)).To(Succeed())

			verifyMigratedPipelineRun(&oldPushPR, &newPushPR, oldCompKey.Name, newCompKey.Name, "main", ".", false, false)
			verifyMigratedPipelineRun(&oldPRPipelineRun, &newPRPipelineRun, oldCompKey.Name, newCompKey.Name, "main", ".", true, false)

			// The shared PaC Repository CR (keyed by git URL, same for both models) is owned by both the
			// old and new Component.
			oldComponent := getComponentOldModel(oldCompKey)
			repository := waitPaCRepositoryCreated(repositoryNameKey)
			Expect(repository.OwnerReferences).To(HaveLen(2))
			Expect([]types.UID{repository.OwnerReferences[0].UID, repository.OwnerReferences[1].UID}).To(ConsistOf(oldComponent.UID, getComponent(newCompKey).UID))
		})
	})

	Context("old Component's pipeline run files removed from the repository before migration runs", func() {
		var (
			namespace  = "prmigration-removed"
			oldCompKey = types.NamespacedName{Name: "comp1-app1-removed", Namespace: namespace}
			newCompKey = types.NamespacedName{Name: "comp1-app1-removed-new", Namespace: namespace}
			gitURL     = SampleRepoLink + "-migration-removed"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(newCompKey)
			deleteComponentOldModel(oldCompKey)
			deleteServiceAccount(getComponentServiceAccountKey(oldCompKey))
			deleteServiceAccount(getComponentServiceAccountKey(newCompKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("does not open a migration merge request, keeps the old Component in migration_map, but clears perform_migration since it's a one-shot trigger", func() {
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				return "https://githost.com/mr/old-1111", nil
			}

			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey:     oldCompKey,
				gitURL:           gitURL,
				gitRevision:      "main",
				gitSourceContext: ".",
				application:      "testapp1",
				annotations:      defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)

			migrationMap := map[string]string{oldCompKey.Name: "main"}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not create a PaC configuration PR for the new Component, since no CreateConfiguration action was requested")
				return "", nil
			}

			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not open a migration merge request, since no pipeline run files were found")
				return "", nil
			}
			// Both pipeline run files were removed from the repository: .tekton is empty.
			fakeTektonDirectoryFiles(map[string][]byte{})

			newComponent := getComponentData(componentConfig{
				componentKey: newCompKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "main", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(newCompKey, 1)

			// Migration wasn't requested yet (perform_migration annotation absent), so migration_map must
			// still carry its original, untouched value.
			expectMigrationAnnotations(newCompKey, false, migrationMap)

			// Request the migration via the perform_migration annotation (an annotation-only update)
			newComponent = getComponent(newCompKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			// No pipeline run files were found, so no migration merge request must ever be opened. This
			// isn't an error, so migration_map keeps the unresolved entry for a later, manually
			// re-requested retry, but perform_migration (a one-shot trigger, not a "keep retrying" flag)
			// is cleared - that's the completion signal to wait for here, since migration_map itself
			// doesn't change.
			Eventually(func() map[string]string {
				return getComponent(newCompKey).Annotations
			}, timeout, interval).ShouldNot(HaveKey(performMigrationAnnotationName))
			expectMigrationAnnotations(newCompKey, false, migrationMap)
		})
	})

	Context("migration merge request creation fails with a permanent error", func() {
		var (
			namespace  = "prmigration-permanent-error"
			oldCompKey = types.NamespacedName{Name: "comp1-app1-permanent", Namespace: namespace}
			newCompKey = types.NamespacedName{Name: "comp1-app1-permanent-new", Namespace: namespace}
			gitURL     = SampleRepoLink + "-migration-permanent-error"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(newCompKey)
			deleteComponentOldModel(oldCompKey)
			deleteServiceAccount(getComponentServiceAccountKey(oldCompKey))
			deleteServiceAccount(getComponentServiceAccountKey(newCompKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("records the error on the Component status, keeps migration_map pending and does not keep retrying", func() {
			oldFilesByPath := map[string][]byte{}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				for _, file := range d.Files {
					oldFilesByPath[file.FullPath] = file.Content
				}
				return "https://githost.com/mr/old-1111", nil
			}

			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey:     oldCompKey,
				gitURL:           gitURL,
				gitRevision:      "main",
				gitSourceContext: ".",
				application:      "testapp1",
				annotations:      defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			Expect(oldFilesByPath).To(HaveLen(2))

			migrationMap := map[string]string{oldCompKey.Name: "main"}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not create a PaC configuration PR for the new Component, since no CreateConfiguration action was requested")
				return "", nil
			}

			migrationCallCount := 0
			permanentErr := boerrors.NewBuildOpError(boerrors.EGitHubBranchDoesntExist, errors.New("target branch does not exist"))
			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				migrationCallCount++
				return "", permanentErr
			}
			fakeTektonDirectoryFiles(oldFilesByPath)

			newComponent := getComponentData(componentConfig{
				componentKey: newCompKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "main", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(newCompKey, 1)

			// Migration wasn't requested yet (perform_migration annotation absent), so migration_map must
			// still carry its original, untouched value.
			expectMigrationAnnotations(newCompKey, false, migrationMap)

			// Request the migration via the perform_migration annotation (an annotation-only update)
			newComponent = getComponent(newCompKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			Eventually(func() int {
				return migrationCallCount
			}, timeout, interval).Should(Equal(1))

			Eventually(func() string {
				return getComponent(newCompKey).Status.Message
			}, timeout, interval).Should(Equal(fmt.Sprintf("Pipeline runs migration: %s", permanentErr.Error())))

			// A permanent error can't be fixed by retrying, so migration_map must still carry the
			// unresolved entry and perform_migration must stay set - but the reconciler must not keep
			// hammering the git provider for it.
			expectMigrationAnnotations(newCompKey, true, migrationMap)
			Consistently(func() int {
				return migrationCallCount
			}, ensureTimeout, interval).Should(Equal(1))
		})
	})

	Context("migration merge request creation fails with a transient error and succeeds on retry", func() {
		var (
			namespace  = "prmigration-transient-error"
			oldCompKey = types.NamespacedName{Name: "comp1-app1-transient", Namespace: namespace}
			newCompKey = types.NamespacedName{Name: "comp1-app1-transient-new", Namespace: namespace}
			gitURL     = SampleRepoLink + "-migration-transient-error"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(newCompKey)
			deleteComponentOldModel(oldCompKey)
			deleteServiceAccount(getComponentServiceAccountKey(oldCompKey))
			deleteServiceAccount(getComponentServiceAccountKey(newCompKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("retries after the transient failure and eventually completes the migration", func() {
			oldFilesByPath := map[string][]byte{}
			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				for _, file := range d.Files {
					oldFilesByPath[file.FullPath] = file.Content
				}
				return "https://githost.com/mr/old-1111", nil
			}

			createComponentAndProcessBuildRequest(componentConfigOldModel{
				componentKey:     oldCompKey,
				gitURL:           gitURL,
				gitRevision:      "main",
				gitSourceContext: ".",
				application:      "testapp1",
				annotations:      defaultPipelineAnnotations,
			}, BuildRequestConfigurePaCAnnotationValue)
			Expect(oldFilesByPath).To(HaveLen(2))
			oldPushPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, false)
			oldPRPath := getPipelineRunDefinitionFilePathOldModel(oldCompKey.Name, true)

			var oldPushPR, oldPRPipelineRun tektonapi.PipelineRun
			Expect(yaml.Unmarshal(oldFilesByPath[oldPushPath], &oldPushPR)).To(Succeed())
			Expect(yaml.Unmarshal(oldFilesByPath[oldPRPath], &oldPRPipelineRun)).To(Succeed())

			migrationMap := map[string]string{oldCompKey.Name: "main"}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			EnsurePaCMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not create a PaC configuration PR for the new Component, since no CreateConfiguration action was requested")
				return "", nil
			}

			migrationCallCount := 0
			var migrationMRData *gp.MergeRequestData
			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				migrationCallCount++
				if migrationCallCount == 1 {
					return "", errors.New("transient git API failure")
				}
				migrationMRData = d
				return "https://githost.com/mr/migrate-5555", nil
			}
			fakeTektonDirectoryFiles(oldFilesByPath)

			newComponent := getComponentData(componentConfig{
				componentKey: newCompKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "main", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(newCompKey, 1)

			// Migration wasn't requested yet (perform_migration annotation absent), so migration_map must
			// still carry its original, untouched value.
			expectMigrationAnnotations(newCompKey, false, migrationMap)

			// Request the migration via the perform_migration annotation (an annotation-only update)
			newComponent = getComponent(newCompKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			// Controller-runtime automatically requeues on a non-nil Reconcile error (the transient
			// error is returned unchanged, since allErrorsPermanent only short-circuits BuildOpErrors).
			// The status message set for the first failure isn't reliably observable here: a normal,
			// successful reconcile pass resets it to "" before the migration step even runs, and that
			// can happen on the very next (retried) reconcile before the test gets a chance to poll it.
			// Once migration_map is gone, migration has definitely completed, so the call count it took to
			// get there (exactly 2: one failure, then a successful retry) is already settled and safe to
			// check.
			Eventually(func() map[string]string {
				return getComponent(newCompKey).Annotations
			}, timeout, interval).ShouldNot(HaveKey(migrationMapAnnotationName))
			expectMigrationAnnotations(newCompKey, false, nil)

			Expect(migrationCallCount).To(Equal(2))

			verifyMigrationMergeRequestFiles(migrationMRData, oldCompKey.Name, newCompKey.Name, "main", "main", ".", &oldPushPR, &oldPRPipelineRun)
		})
	})

	Context("Component requests migration with a malformed migration_map annotation", func() {
		var (
			namespace = "prmigration-malformed-map"
			compKey   = types.NamespacedName{Name: "comp1-malformed-map", Namespace: namespace}
			gitURL    = SampleRepoLink + "-migration-malformed-map"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(compKey)
			deleteServiceAccount(getComponentServiceAccountKey(compKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("records a parse error on the Component status", func() {
			brokenMigrationMap := "not-valid-json"
			var dummy map[string]string
			parseErr := json.Unmarshal([]byte(brokenMigrationMap), &dummy)
			Expect(parseErr).To(HaveOccurred())

			newComponent := getComponentData(componentConfig{
				componentKey: compKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "main", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
				},
				annotations: map[string]string{
					migrationMapAnnotationName: brokenMigrationMap,
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(compKey, 1)

			// Request the migration via the perform_migration annotation (an annotation-only update),
			// same as the other migration specs.
			newComponent = getComponent(compKey)
			newComponent.Annotations[performMigrationAnnotationName] = "true"
			Expect(k8sClient.Update(ctx, newComponent)).To(Succeed())

			Eventually(func() string {
				return getComponent(compKey).Status.Message
			}, timeout, interval).Should(Equal(fmt.Sprintf("Pipeline runs migration: failed to parse migration_map annotation: %s", parseErr)))

			// A parse failure can't be fixed by retrying as-is, so both migration annotations must stay
			// in place, untouched, for a later, manually corrected retry.
			annotations := getComponent(compKey).Annotations
			Expect(annotations).To(HaveKeyWithValue(performMigrationAnnotationName, "true"))
			Expect(annotations).To(HaveKeyWithValue(migrationMapAnnotationName, brokenMigrationMap))
		})
	})

	Context("migration_map entries dropped for versions that no longer need migrating", func() {
		var (
			namespace = "prmigration-filtering"
			compKey   = types.NamespacedName{Name: "comp1-filtering", Namespace: namespace}
			gitURL    = SampleRepoLink + "-migration-filtering"

			repositoryNameKey types.NamespacedName
		)

		BeforeEach(func() {
			createNamespace(namespace)
			repoName, err := generatePaCRepositoryNameFromGitUrl(gitURL)
			Expect(err).NotTo(HaveOccurred())
			repositoryNameKey = types.NamespacedName{Name: repoName, Namespace: namespace}
		})

		AfterEach(func() {
			deleteComponent(compKey)
			deleteServiceAccount(getComponentServiceAccountKey(compKey))
			deleteRoleBinding(types.NamespacedName{Name: buildPipelineRoleBindingName, Namespace: namespace})
			deletePaCRepository(repositoryNameKey)
		})

		It("drops every entry and clears both migration annotations without performing any migration", func() {
			// Three old components map onto: a version that will get a create-configuration-PR action
			// requested for it (v1), a version that will be removed from the spec (v2), and a version
			// that was never in the spec at all (v3).
			migrationMap := map[string]string{"old-v1": "v1", "old-v2": "v2", "old-v3": "v3"}
			migrationMapJson, err := json.Marshal(migrationMap)
			Expect(err).NotTo(HaveOccurred())

			newComponent := getComponentData(componentConfig{
				componentKey: compKey,
				gitURL:       gitURL,
				versions: []compv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
					{Name: "v2", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
				},
				skipOffboardingPr: true,
				annotations: map[string]string{
					migrationMapAnnotationName: string(migrationMapJson),
				},
			})
			createComponent(newComponent)

			waitForComponentStatusVersions(compKey, 2)

			// Migration wasn't requested yet (perform_migration annotation absent), so migration_map must
			// still carry its original, untouched value - including the never-existed "v3" entry.
			expectMigrationAnnotations(compKey, false, migrationMap)

			EnsureMigrationMergeRequestFunc = func(repoUrl string, d *gp.MergeRequestData) (string, error) {
				defer GinkgoRecover()
				Fail("should not open a migration merge request, since every migration_map entry must be dropped")
				return "", nil
			}

			// Request the migration, remove v2 from the spec, and request a create-configuration PR for
			// v1 - all in one update, so the filtering in Reconcile has to drop all three migration_map
			// entries on the very same pass: v1 is being configured with the new model, v2 (and the
			// never-existed v3) no longer exist in the spec.
			component := getComponent(compKey)
			component.Annotations[performMigrationAnnotationName] = "true"
			component.Spec.Source.Versions = []compv1alpha1.ComponentVersion{
				{Name: "v1", Revision: "main", Context: ".", DockerfilePath: "docker/Dockerfile"},
			}
			component.Spec.Actions.CreateConfiguration.Version = "v1"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			// Every entry was dropped, so migration_map ends up empty and - per updateMigrationState -
			// both migration annotations are removed entirely, not just perform_migration.
			Eventually(func() map[string]string {
				return getComponent(compKey).Annotations
			}, timeout, interval).ShouldNot(HaveKey(migrationMapAnnotationName))
			expectMigrationAnnotations(compKey, false, nil)

			// v2's offboarding and v1's create-configuration PR both complete normally alongside the
			// migration_map filtering (migration_map clears before those finish onboarding/offboarding,
			// so this needs its own wait) - only v1 remains in status, and no error was left behind.
			waitForComponentStatusVersions(compKey, 1)
			waitForComponentStatusMessage(compKey, true)

			// CreateConfiguration action is cleared once its PR is created (all 3 variants).
			Eventually(func() bool {
				actions := getComponent(compKey).Spec.Actions.CreateConfiguration
				return !actions.AllVersions && actions.Version == "" && len(actions.Versions) == 0
			}, timeout, interval).Should(BeTrue())
		})
	})
})
