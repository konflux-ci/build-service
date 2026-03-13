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

package controllers

import (
	"context"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
)

func TestSanitizeVersionName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "should convert to lowercase and replace dots and underscores with hyphens",
			input:    "V1.0_BETA",
			expected: "v1-0-beta",
		},
		{
			name:     "should remove leading and trailing hyphens",
			input:    "__v1.0__",
			expected: "v1-0",
		},
		{
			name:     "should handle empty result after sanitization",
			input:    "___",
			expected: "",
		},
		{
			name:     "should handle already sanitized name",
			input:    "v1-0",
			expected: "v1-0",
		},
		{
			name:     "should remove special characters",
			input:    "v1@#$0",
			expected: "v1---0",
		},
		{
			name:     "should replace underscores with hyphens",
			input:    "v1___0",
			expected: "v1---0",
		},
		{
			name:     "should handle empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeVersionName(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestValidateVersions(t *testing.T) {
	getComponent := func(versions []compapiv1alpha1.ComponentVersion) compapiv1alpha1.Component {
		component := compapiv1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testcomponent",
				Namespace: "workspace-name",
			},
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitURL: "https://github.com/user/repo.git",
					},
				},
			},
		}
		if versions != nil {
			component.Spec.Source.Versions = versions
		}
		return component
	}

	tests := []struct {
		name        string
		expectError bool
		errorSubstr string
		versions    []compapiv1alpha1.ComponentVersion
	}{
		{
			name: "should accept valid versions",
			versions: []compapiv1alpha1.ComponentVersion{
				{Name: "v1.0", Revision: "main"},
				{Name: "v2.0", Revision: "develop"},
			},
			expectError: false,
		},
		{
			name: "should validate version names are required",
			versions: []compapiv1alpha1.ComponentVersion{
				{Name: "", Revision: "main"},
			},
			expectError: true,
			errorSubstr: "spec.source.versions[0].name is required",
		},
		{
			name: "should validate version revisions are required",
			versions: []compapiv1alpha1.ComponentVersion{
				{Name: "v1.0", Revision: ""},
			},
			expectError: true,
			errorSubstr: "spec.source.versions[0].revision is required",
		},
		{
			name: "should detect duplicate sanitized version names",
			versions: []compapiv1alpha1.ComponentVersion{
				{Name: "v1.0", Revision: "main"},
				{Name: "v1_0", Revision: "develop"},
			},
			expectError: true,
			errorSubstr: "conflicts with version",
		},
		{
			name: "should detect empty sanitized version name",
			versions: []compapiv1alpha1.ComponentVersion{
				{Name: "___", Revision: "main"},
			},
			expectError: true,
			errorSubstr: "becomes empty after sanitization",
		},
		{
			name: "should validate version revisions are required, while 2 versions are valid",
			versions: []compapiv1alpha1.ComponentVersion{
				{Name: "v1.0", Revision: "main"},
				{Name: "v2.0", Revision: "develop"},
				{Name: "v3.0", Revision: ""},
			},
			expectError: true,
			errorSubstr: "spec.source.versions[2].revision is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := getComponent(tt.versions)
			errors := validateVersions(&component)
			if tt.expectError {
				assert.Assert(t, len(errors) > 0, "expected errors, got none")
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			} else {
				assert.Equal(t, 0, len(errors), "expected no errors, got %d: %v", len(errors), errors)
			}
		})
	}
}

func TestValidatePipelineConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		pipeline    compapiv1alpha1.ComponentBuildPipeline
		versionName string
		wantErrors  int
		errorSubstr string
	}{
		{
			name: "should validate that PullAndPush cannot be used with Pull or Push",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				PullAndPush: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "cannot specify pull-and-push together with pull or push",
		},
		{
			name: "should validate that PullAndPush cannot be used with Push",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				PullAndPush: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
				Push: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "cannot specify pull-and-push together with pull or push",
		},
		{
			name: "should validate PipelineRefGit required fields - missing pathInRepo",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefGit: compapiv1alpha1.PipelineRefGit{
						Url:      "https://github.com/test/pipelines",
						Revision: "main",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "pathInRepo is required",
		},
		{
			name: "should validate PipelineRefGit required fields - missing url and revision",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefGit: compapiv1alpha1.PipelineRefGit{
						PathInRepo: ".tekton/pipeline.yaml",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  2,
			errorSubstr: "is required",
		},
		{
			name: "should validate PipelineSpecFromBundle required fields",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Push: compapiv1alpha1.PipelineDefinition{
					PipelineSpecFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
						Bundle: "quay.io/repo/bundle:latest",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "name is required",
		},
		{
			name: "should validate multiple definition types cannot be used together",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
					PipelineRefGit: compapiv1alpha1.PipelineRefGit{
						Url:        "https://github.com/test/pipelines",
						PathInRepo: ".tekton/pipeline.yaml",
						Revision:   "main",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "has multiple definitions specified",
		},
		{
			name:        "should accept empty pipeline configuration",
			pipeline:    compapiv1alpha1.ComponentBuildPipeline{},
			versionName: "test-version",
			wantErrors:  0,
		},
		{
			name: "should accept valid pipeline with PipelineRefName",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
				Push: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			versionName: "test-version",
			wantErrors:  0,
		},
		{
			name: "should accept valid pipeline with PullAndPush using PipelineRefGit",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				PullAndPush: compapiv1alpha1.PipelineDefinition{
					PipelineRefGit: compapiv1alpha1.PipelineRefGit{
						Url:        "https://github.com/test/pipelines",
						PathInRepo: ".tekton/pipeline.yaml",
						Revision:   "main",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validatePipelineConfiguration(tt.pipeline, tt.versionName)
			assert.Equal(t, tt.wantErrors, len(errors), "expected %d errors, got %d: %v", tt.wantErrors, len(errors), errors)
			if tt.wantErrors > 0 && tt.errorSubstr != "" {
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			}
		})
	}
}

func TestBuildVersionInfoMap(t *testing.T) {
	tests := []struct {
		name       string
		component  *compapiv1alpha1.Component
		fromStatus bool
		validate   func(t *testing.T, result map[string]*VersionInfo)
	}{
		{
			name: "should build version info map from spec with all fields",
			component: &compapiv1alpha1.Component{
				Spec: compapiv1alpha1.ComponentSpec{
					Source: compapiv1alpha1.ComponentSource{
						ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
							DockerfileURI: "Dockerfile.default",
							Versions: []compapiv1alpha1.ComponentVersion{
								{Name: "v1.0", Revision: "main", Context: "app1"},
								{Name: "v2.0", Revision: "develop", DockerfileURI: "Dockerfile.custom"},
							},
						},
					},
				},
			},
			fromStatus: false,
			validate: func(t *testing.T, result map[string]*VersionInfo) {
				assert.Equal(t, 2, len(result))
				assert.Equal(t, "v1.0", result["v1.0"].OriginalVersion)
				assert.Equal(t, "v1-0", result["v1.0"].SanitizedVersion)
				assert.Equal(t, "main", result["v1.0"].Revision)
				assert.Equal(t, "app1", result["v1.0"].Context)
				assert.Equal(t, "Dockerfile.default", result["v1.0"].DockerfileURI)

				assert.Equal(t, "v2.0", result["v2.0"].OriginalVersion)
				assert.Equal(t, "Dockerfile.custom", result["v2.0"].DockerfileURI)
			},
		},
		{
			name: "should build version info map from spec without DockerfileURI and default to Dockerfile",
			component: &compapiv1alpha1.Component{
				Spec: compapiv1alpha1.ComponentSpec{
					Source: compapiv1alpha1.ComponentSource{
						ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
							Versions: []compapiv1alpha1.ComponentVersion{
								{Name: "v1.0", Revision: "main"},
								{Name: "v2.0", Revision: "develop", Context: "app2"},
							},
						},
					},
				},
			},
			fromStatus: false,
			validate: func(t *testing.T, result map[string]*VersionInfo) {
				assert.Equal(t, 2, len(result))
				assert.Equal(t, "v1.0", result["v1.0"].OriginalVersion)
				assert.Equal(t, "v1-0", result["v1.0"].SanitizedVersion)
				assert.Equal(t, "main", result["v1.0"].Revision)
				assert.Equal(t, "Dockerfile", result["v1.0"].DockerfileURI)

				assert.Equal(t, "v2.0", result["v2.0"].OriginalVersion)
				assert.Equal(t, "Dockerfile", result["v2.0"].DockerfileURI)
				assert.Equal(t, "app2", result["v2.0"].Context)
			},
		},
		{
			name: "should build version info map from status",
			component: &compapiv1alpha1.Component{
				Status: compapiv1alpha1.ComponentStatus{
					Versions: []compapiv1alpha1.ComponentVersionStatus{
						{Name: "v1.0", Revision: "main"},
						{Name: "v2.0", Revision: "develop"},
					},
				},
			},
			fromStatus: true,
			validate: func(t *testing.T, result map[string]*VersionInfo) {
				assert.Equal(t, 2, len(result))
				assert.Equal(t, "v1.0", result["v1.0"].OriginalVersion)
				assert.Equal(t, "v1-0", result["v1.0"].SanitizedVersion)
				assert.Equal(t, "main", result["v1.0"].Revision)
				// Status-based map should not include Context or DockerfileURI
				assert.Equal(t, "", result["v1.0"].Context)
				assert.Equal(t, "", result["v1.0"].DockerfileURI)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildVersionInfoMap(tt.component, tt.fromStatus)
			tt.validate(t, result)
		})
	}
}

func TestEqualRepositorySettings(t *testing.T) {
	tests := []struct {
		name      string
		settings1 compapiv1alpha1.RepositorySettings
		settings2 compapiv1alpha1.RepositorySettings
		expected  bool
	}{
		{
			name: "should detect different comment strategies",
			settings1: compapiv1alpha1.RepositorySettings{
				CommentStrategy: "",
			},
			settings2: compapiv1alpha1.RepositorySettings{
				CommentStrategy: "disable_all",
			},
			expected: false,
		},
		{
			name: "should detect different GitHub app token scope repos",
			settings1: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1"},
			},
			expected: false,
		},
		{
			name: "should consider different ordering of GitHub app token scope repos as equal",
			settings1: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo2", "repo1"},
			},
			expected: true,
		},
		{
			name: "should ignore duplicates when comparing GitHub app token scope repos",
			settings1: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo2", "repo1"},
			},
			expected: true,
		},
		{
			name: "should detect different repos despite duplicates",
			settings1: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo3", "repo3"},
			},
			expected: false,
		},
		{
			name: "should consider equal settings as equal",
			settings1: compapiv1alpha1.RepositorySettings{
				CommentStrategy:          "",
				GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				CommentStrategy:          "",
				GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
			},
			expected: true,
		},
		{
			name:      "should consider empty settings as equal",
			settings1: compapiv1alpha1.RepositorySettings{},
			settings2: compapiv1alpha1.RepositorySettings{},
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := equalRepositorySettings(tt.settings1, tt.settings2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetVersionsForAction(t *testing.T) {
	existingVersions := map[string]*VersionInfo{
		"v1": {OriginalVersion: "v1", SanitizedVersion: "v1", Revision: "main"},
		"v2": {OriginalVersion: "v2", SanitizedVersion: "v2", Revision: "develop"},
	}

	tests := []struct {
		name             string
		allVersions      bool
		singleVersion    string
		multipleVersions []string
		wantValid        []string
		wantInvalid      []string
	}{
		{
			name:        "should return all versions when AllVersions is true",
			allVersions: true,
			wantValid:   []string{"v1", "v2"},
			wantInvalid: []string{},
		},
		{
			name:             "should return all versions when AllVersions is true but still check for invalid versions",
			allVersions:      true,
			singleVersion:    "invalid1",
			multipleVersions: []string{"v2", "invalid2"},
			wantValid:        []string{"v1", "v2"},
			wantInvalid:      []string{"invalid1", "invalid2"},
		},
		{
			name:          "should return single version when specified",
			singleVersion: "v1",
			wantValid:     []string{"v1"},
			wantInvalid:   []string{},
		},
		{
			name:             "should return multiple versions when specified",
			multipleVersions: []string{"v1", "v2"},
			wantValid:        []string{"v1", "v2"},
			wantInvalid:      []string{},
		},
		{
			name:             "should filter out invalid versions",
			multipleVersions: []string{"v1", "v3", "v4"},
			wantValid:        []string{"v1"},
			wantInvalid:      []string{"v3", "v4"},
		},
		{
			name:             "should remove duplicates and combine single and multiple",
			singleVersion:    "v1",
			multipleVersions: []string{"v1", "v2"},
			wantValid:        []string{"v1", "v2"},
			wantInvalid:      []string{},
		},
		{
			name:          "should identify invalid single version",
			singleVersion: "nonexistent",
			wantValid:     []string{},
			wantInvalid:   []string{"nonexistent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validVersions, invalidVersions := getUniqueVersionsFromVersionFields(
				tt.allVersions,
				tt.singleVersion,
				tt.multipleVersions,
				existingVersions,
			)

			assert.Equal(t, len(tt.wantValid), len(validVersions), "valid versions count mismatch")
			assert.Equal(t, len(tt.wantInvalid), len(invalidVersions), "invalid versions count mismatch")

			for _, v := range tt.wantValid {
				assert.Assert(t, slices.Contains(validVersions, v), "expected valid version %s not found", v)
			}
			for _, v := range tt.wantInvalid {
				assert.Assert(t, slices.Contains(invalidVersions, v), "expected invalid version %s not found", v)
			}
		})
	}
}

func TestHasPipelineRefGit(t *testing.T) {
	tests := []struct {
		name     string
		refGit   compapiv1alpha1.PipelineRefGit
		expected bool
	}{
		{
			name: "should return true when all fields set",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url:        "https://github.com/test/repo",
				PathInRepo: ".tekton/pipeline.yaml",
				Revision:   "main",
			},
			expected: true,
		},
		{
			name: "should return true when only url set",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url: "https://github.com/test/repo",
			},
			expected: true,
		},
		{
			name:     "should return false when all fields empty",
			refGit:   compapiv1alpha1.PipelineRefGit{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineRefGit(tt.refGit)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasPipelineRefName(t *testing.T) {
	tests := []struct {
		name     string
		refName  string
		expected bool
	}{
		{
			name:     "should return true for non-empty name",
			refName:  "docker-build",
			expected: true,
		},
		{
			name:     "should return false for empty name",
			refName:  "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineRefName(tt.refName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasPipelineSpecFromBundle(t *testing.T) {
	tests := []struct {
		name           string
		specFromBundle compapiv1alpha1.PipelineSpecFromBundle
		expected       bool
	}{
		{
			name: "should return true when both fields set",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Bundle: "quay.io/repo/bundle:latest",
				Name:   "docker-build",
			},
			expected: true,
		},
		{
			name: "should return true when only bundle set",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Bundle: "quay.io/repo/bundle:latest",
			},
			expected: true,
		},
		{
			name:           "should return false when all fields empty",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{},
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineSpecFromBundle(tt.specFromBundle)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidatePipelineRefGitFields(t *testing.T) {
	tests := []struct {
		name        string
		refGit      compapiv1alpha1.PipelineRefGit
		errorPrefix string
		wantErrors  int
		errorSubstr string
	}{
		{
			name: "should accept all fields set",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url:        "https://github.com/test/repo",
				PathInRepo: ".tekton/pipeline.yaml",
				Revision:   "main",
			},
			errorPrefix: "test",
			wantErrors:  0,
		},
		{
			name: "should reject missing url",
			refGit: compapiv1alpha1.PipelineRefGit{
				PathInRepo: ".tekton/pipeline.yaml",
				Revision:   "main",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "url is required",
		},
		{
			name: "should reject missing pathInRepo",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url:      "https://github.com/test/repo",
				Revision: "main",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "pathInRepo is required",
		},
		{
			name: "should reject missing revision",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url:        "https://github.com/test/repo",
				PathInRepo: ".tekton/pipeline.yaml",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "revision is required",
		},
		{
			name:        "should reject all missing fields",
			refGit:      compapiv1alpha1.PipelineRefGit{},
			errorPrefix: "version v1",
			wantErrors:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validatePipelineRefGitFields(tt.refGit, tt.errorPrefix)
			assert.Equal(t, tt.wantErrors, len(errors), "expected %d errors, got %d: %v", tt.wantErrors, len(errors), errors)
			if tt.wantErrors > 0 && tt.errorSubstr != "" {
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			}
		})
	}
}

func TestValidatePipelineSpecFromBundleFields(t *testing.T) {
	tests := []struct {
		name           string
		specFromBundle compapiv1alpha1.PipelineSpecFromBundle
		errorPrefix    string
		wantErrors     int
		errorSubstr    string
	}{
		{
			name: "should accept both fields set",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Bundle: "quay.io/repo/bundle:latest",
				Name:   "docker-build",
			},
			errorPrefix: "test",
			wantErrors:  0,
		},
		{
			name: "should reject missing bundle",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Name: "docker-build",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "bundle is required",
		},
		{
			name: "should reject missing name",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Bundle: "quay.io/repo/bundle:latest",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "name is required",
		},
		{
			name:           "should reject all missing fields",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{},
			errorPrefix:    "version v1",
			wantErrors:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validatePipelineSpecFromBundleFields(tt.specFromBundle, tt.errorPrefix)
			assert.Equal(t, tt.wantErrors, len(errors), "expected %d errors, got %d: %v", tt.wantErrors, len(errors), errors)
			if tt.wantErrors > 0 && tt.errorSubstr != "" {
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			}
		})
	}
}

func TestHasPipelineDefConfig(t *testing.T) {
	tests := []struct {
		name        string
		pipelineDef compapiv1alpha1.PipelineDefinition
		expected    bool
	}{
		{
			name: "should return true for PipelineRefGit",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineRefGit: compapiv1alpha1.PipelineRefGit{
					Url: "https://github.com/test/repo",
				},
			},
			expected: true,
		},
		{
			name: "should return true for PipelineRefName",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineRefName: "docker-build",
			},
			expected: true,
		},
		{
			name: "should return true for PipelineSpecFromBundle",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineSpecFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
					Bundle: "quay.io/repo/bundle:latest",
				},
			},
			expected: true,
		},
		{
			name:        "should return false for empty definition",
			pipelineDef: compapiv1alpha1.PipelineDefinition{},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineDefConfig(tt.pipelineDef)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasPipelineConfig(t *testing.T) {
	tests := []struct {
		name     string
		pipeline compapiv1alpha1.ComponentBuildPipeline
		expected bool
	}{
		{
			name: "should return true for PullAndPush",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				PullAndPush: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			expected: true,
		},
		{
			name: "should return true for Pull",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			expected: true,
		},
		{
			name: "should return true for Push",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Push: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			expected: true,
		},
		{
			name:     "should return false for empty pipeline",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineConfig(tt.pipeline)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractPipelineDef(t *testing.T) {
	tests := []struct {
		name        string
		pipelineDef compapiv1alpha1.PipelineDefinition
		validate    func(t *testing.T, result *PipelineDef)
	}{
		{
			name: "should extract PipelineRefGit",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineRefGit: compapiv1alpha1.PipelineRefGit{
					Url:        "https://github.com/test/repo",
					PathInRepo: ".tekton/pipeline.yaml",
					Revision:   "main",
				},
			},
			validate: func(t *testing.T, result *PipelineDef) {
				assert.Assert(t, result.PipelineRefGit != nil)
				assert.Equal(t, "https://github.com/test/repo", result.PipelineRefGit.Url)
				assert.Equal(t, "", result.PipelineRefName)
				assert.Assert(t, result.PipelineSpecFromBundle == nil)
			},
		},
		{
			name: "should extract PipelineRefName",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineRefName: "docker-build",
			},
			validate: func(t *testing.T, result *PipelineDef) {
				assert.Equal(t, "docker-build", result.PipelineRefName)
				assert.Assert(t, result.PipelineRefGit == nil)
				assert.Assert(t, result.PipelineSpecFromBundle == nil)
			},
		},
		{
			name: "should extract PipelineSpecFromBundle",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineSpecFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
					Bundle: "quay.io/repo/bundle:latest",
					Name:   "docker-build",
				},
			},
			validate: func(t *testing.T, result *PipelineDef) {
				assert.Assert(t, result.PipelineSpecFromBundle != nil)
				assert.Equal(t, "quay.io/repo/bundle:latest", result.PipelineSpecFromBundle.Bundle)
				assert.Assert(t, result.PipelineRefGit == nil)
				assert.Equal(t, "", result.PipelineRefName)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPipelineDef(tt.pipelineDef)
			tt.validate(t, result)
		})
	}
}

func TestValidatePipelines(t *testing.T) {
	tests := []struct {
		name              string
		component         *compapiv1alpha1.Component
		wantErrors        int
		wantPipelineCount int
		errorSubstr       string
		wantPipelines     map[string]*VersionPipelineDefinition
	}{
		{
			name: "should validate and extract pipelines from default and versions",
			component: &compapiv1alpha1.Component{
				Spec: compapiv1alpha1.ComponentSpec{
					DefaultBuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
						Pull: compapiv1alpha1.PipelineDefinition{PipelineRefName: "docker-build"},
					},
					Source: compapiv1alpha1.ComponentSource{
						ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
							Versions: []compapiv1alpha1.ComponentVersion{
								{Name: "v1", Revision: "main"},
								{Name: "v2", Revision: "develop"},
							}}}},
			},
			wantErrors:        0,
			wantPipelineCount: 2, // both versions inherit from default
			wantPipelines: map[string]*VersionPipelineDefinition{
				"v1": {
					Pull: &PipelineDef{PipelineRefName: "docker-build"}, // from default
					Push: nil,
				},
				"v2": {
					Pull: &PipelineDef{PipelineRefName: "docker-build"}, // from default
					Push: nil,
				},
			},
		},
		{
			name: "should merge default pipeline with version-specific pipeline",
			component: &compapiv1alpha1.Component{
				Spec: compapiv1alpha1.ComponentSpec{
					DefaultBuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
						Pull: compapiv1alpha1.PipelineDefinition{PipelineRefName: "default-pull"},
					},
					Source: compapiv1alpha1.ComponentSource{
						ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
							Versions: []compapiv1alpha1.ComponentVersion{
								{
									Name:     "v1",
									Revision: "main",
									BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
										Push: compapiv1alpha1.PipelineDefinition{PipelineRefName: "custom-push"},
									},
								},
								{Name: "v2", Revision: "develop"},
							}}}},
			},
			wantErrors:        0,
			wantPipelineCount: 2,
			wantPipelines: map[string]*VersionPipelineDefinition{
				"v1": {
					Pull: &PipelineDef{PipelineRefName: "default-pull"}, // from default
					Push: &PipelineDef{PipelineRefName: "custom-push"},  // from version
				},
				"v2": {
					Pull: &PipelineDef{PipelineRefName: "default-pull"}, // from default
					Push: nil,
				},
			},
		},
		{
			name: "should handle default PullAndPush with version-specific Pull, Push, and PullAndPush",
			component: &compapiv1alpha1.Component{
				Spec: compapiv1alpha1.ComponentSpec{
					DefaultBuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
						PullAndPush: compapiv1alpha1.PipelineDefinition{PipelineRefName: "default-pullandpush"},
					},
					Source: compapiv1alpha1.ComponentSource{
						ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
							Versions: []compapiv1alpha1.ComponentVersion{
								{
									Name:     "v1",
									Revision: "main",
									BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
										Pull: compapiv1alpha1.PipelineDefinition{PipelineRefName: "custom-pull"},
									},
								},
								{
									Name:     "v2",
									Revision: "develop",
									BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
										Push: compapiv1alpha1.PipelineDefinition{PipelineRefName: "custom-push"},
									},
								},
								{
									Name:     "v3",
									Revision: "release",
									BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
										PullAndPush: compapiv1alpha1.PipelineDefinition{PipelineRefName: "custom-pullandpush"},
									},
								},
								{Name: "v4", Revision: "staging"},
							}}}},
			},
			wantErrors:        0,
			wantPipelineCount: 4,
			wantPipelines: map[string]*VersionPipelineDefinition{
				"v1": {
					Pull: &PipelineDef{PipelineRefName: "custom-pull"}, // from version
					Push: nil,                                          // default PullAndPush ignored when version has Pull/Push
				},
				"v2": {
					Pull: nil,                                          // default PullAndPush ignored when version has Pull/Push
					Push: &PipelineDef{PipelineRefName: "custom-push"}, // from version
				},
				"v3": {
					Pull: &PipelineDef{PipelineRefName: "custom-pullandpush"}, // from version PullAndPush
					Push: &PipelineDef{PipelineRefName: "custom-pullandpush"}, // from version PullAndPush (same as Pull)
				},
				"v4": {
					Pull: &PipelineDef{PipelineRefName: "default-pullandpush"}, // from default PullAndPush
					Push: &PipelineDef{PipelineRefName: "default-pullandpush"}, // from default PullAndPush (same as Pull)
				},
			},
		},
		{
			name: "should detect validation errors in default pipeline",
			component: &compapiv1alpha1.Component{
				Spec: compapiv1alpha1.ComponentSpec{
					DefaultBuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
						PullAndPush: compapiv1alpha1.PipelineDefinition{PipelineRefName: "docker-build"},
						Pull:        compapiv1alpha1.PipelineDefinition{PipelineRefName: "docker-build"},
					},
					Source: compapiv1alpha1.ComponentSource{
						ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
							Versions: []compapiv1alpha1.ComponentVersion{
								{Name: "v1", Revision: "main"},
							}}}},
			},
			wantErrors:  1,
			errorSubstr: "cannot specify pull-and-push together with pull or push",
		},
		{
			name: "should detect validation errors in version-specific pipeline",
			component: &compapiv1alpha1.Component{
				Spec: compapiv1alpha1.ComponentSpec{
					Source: compapiv1alpha1.ComponentSource{
						ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
							Versions: []compapiv1alpha1.ComponentVersion{
								{
									Name:     "v1",
									Revision: "main",
									BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
										Pull: compapiv1alpha1.PipelineDefinition{
											PipelineRefGit: compapiv1alpha1.PipelineRefGit{
												Url: "https://github.com/test/repo",
												// Missing required fields
											},
										}}}}}}},
			},
			wantErrors:  2, // missing pathInRepo and revision
			errorSubstr: "is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors, pipelines := validatePipelines(tt.component)
			assert.Equal(t, tt.wantErrors, len(errors), "expected %d errors, got %d: %v", tt.wantErrors, len(errors), errors)
			if tt.wantErrors > 0 && tt.errorSubstr != "" {
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			}
			if tt.wantErrors == 0 {
				assert.Equal(t, tt.wantPipelineCount, len(pipelines), "expected %d pipelines, got %d", tt.wantPipelineCount, len(pipelines))
				if tt.wantPipelines != nil {
					for versionName, wantPipeline := range tt.wantPipelines {
						gotPipeline := pipelines[versionName]
						assert.Assert(t, gotPipeline != nil, "pipeline for version %s should exist", versionName)

						// Compare Pull pipeline
						if wantPipeline.Pull == nil {
							assert.Assert(t, gotPipeline.Pull == nil, "version %s Pull should be nil", versionName)
						} else {
							assert.Assert(t, gotPipeline.Pull != nil, "version %s Pull should not be nil", versionName)
							assert.Equal(t, wantPipeline.Pull.PipelineRefName, gotPipeline.Pull.PipelineRefName, "version %s Pull pipeline mismatch", versionName)
						}

						// Compare Push pipeline
						if wantPipeline.Push == nil {
							assert.Assert(t, gotPipeline.Push == nil, "version %s Push should be nil", versionName)
						} else {
							assert.Assert(t, gotPipeline.Push != nil, "version %s Push should not be nil", versionName)
							assert.Equal(t, wantPipeline.Push.PipelineRefName, gotPipeline.Push.PipelineRefName, "version %s Push pipeline mismatch", versionName)
						}
					}
				}
			}
		})
	}
}

func TestDetermineVersionsToCreateConfiguration(t *testing.T) {
	getComponent := func(allVersions bool, version string, versions []string) compapiv1alpha1.Component {
		component := compapiv1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testcomponent",
				Namespace: "workspace-name",
			},
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitURL: "https://github.com/user/repo.git",
					},
				},
			},
		}
		if allVersions {
			component.Spec.Actions.CreateConfiguration.AllVersions = true
		}
		if version != "" {
			component.Spec.Actions.CreateConfiguration.Version = version
		}
		if versions != nil {
			component.Spec.Actions.CreateConfiguration.Versions = versions
		}
		return component
	}

	existingVersions := map[string]*VersionInfo{
		"v1": {OriginalVersion: "v1", SanitizedVersion: "v1", Revision: "main"},
		"v2": {OriginalVersion: "v2", SanitizedVersion: "v2", Revision: "develop"},
	}

	tests := []struct {
		name                             string
		component                        compapiv1alpha1.Component
		wantVersionsToCreatePRFor        []string
		wantInvalidVersionsToCreatePRFor []string
	}{
		{
			name:                             "should return all versions with AllVersions flag",
			component:                        getComponent(true, "", nil),
			wantVersionsToCreatePRFor:        []string{"v1", "v2"},
			wantInvalidVersionsToCreatePRFor: []string{},
		},
		{
			name:                             "should return version from version",
			component:                        getComponent(false, "v1", nil),
			wantVersionsToCreatePRFor:        []string{"v1"},
			wantInvalidVersionsToCreatePRFor: []string{},
		},
		{
			name:                             "should filter invalid versions",
			component:                        getComponent(false, "", []string{"v1", "v3"}),
			wantVersionsToCreatePRFor:        []string{"v1"},
			wantInvalidVersionsToCreatePRFor: []string{"v3"},
		},
		{
			name:                             "should return empty when no action specified",
			component:                        getComponent(false, "", nil),
			wantVersionsToCreatePRFor:        []string{},
			wantInvalidVersionsToCreatePRFor: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ComponentBuildReconciler{}
			versionsToCreatePRFor, invalidVersionsToCreatePRFor := reconciler.determineVersionsToCreateConfigurationFor(context.TODO(), &tt.component, existingVersions)

			assert.Equal(t, len(tt.wantVersionsToCreatePRFor), len(versionsToCreatePRFor), "versions to create PR for count mismatch")
			assert.Equal(t, len(tt.wantInvalidVersionsToCreatePRFor), len(invalidVersionsToCreatePRFor), "invalid versions to create PR for count mismatch")

			for _, v := range tt.wantVersionsToCreatePRFor {
				assert.Assert(t, slices.Contains(versionsToCreatePRFor, v), "expected version to create PR for %s not found", v)
			}
			for _, v := range tt.wantInvalidVersionsToCreatePRFor {
				assert.Assert(t, slices.Contains(invalidVersionsToCreatePRFor, v), "expected invalid version to create PR for %s not found", v)
			}
		})
	}
}

func TestDetermineVersionsToOnboardAndOffboard(t *testing.T) {
	getComponent := func(specVersions []compapiv1alpha1.ComponentVersion, statusVersions []compapiv1alpha1.ComponentVersionStatus) compapiv1alpha1.Component {
		component := compapiv1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testcomponent",
				Namespace: "workspace-name",
			},
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitURL:   "https://github.com/user/repo.git",
						Versions: specVersions,
					},
				},
			},
			Status: compapiv1alpha1.ComponentStatus{
				Versions: statusVersions,
			},
		}
		return component
	}

	tests := []struct {
		name                   string
		component              compapiv1alpha1.Component
		wantVersionsToOnboard  []string
		wantVersionsToOffboard []string
	}{
		{
			name: "should identify versions to onboard",
			component: getComponent(
				[]compapiv1alpha1.ComponentVersion{{Name: "v1", Revision: "main"}, {Name: "v2", Revision: "develop"}},
				[]compapiv1alpha1.ComponentVersionStatus{{Name: "v1", Revision: "main"}}),
			wantVersionsToOnboard:  []string{"v2"},
			wantVersionsToOffboard: []string{},
		},
		{
			name: "should identify all versions to onboard when status is empty",
			component: getComponent(
				[]compapiv1alpha1.ComponentVersion{{Name: "v1", Revision: "main"}, {Name: "v2", Revision: "develop"}},
				nil),
			wantVersionsToOnboard:  []string{"v1", "v2"},
			wantVersionsToOffboard: []string{},
		},
		{
			name: "should identify versions to offboard",
			component: getComponent(
				[]compapiv1alpha1.ComponentVersion{{Name: "v1", Revision: "main"}},
				[]compapiv1alpha1.ComponentVersionStatus{{Name: "v1", Revision: "main"}, {Name: "v2", Revision: "develop"}},
			),
			wantVersionsToOnboard:  []string{},
			wantVersionsToOffboard: []string{"v2"},
		},
		{
			name: "should identify both versions to onboard and offboard simultaneously",
			component: getComponent(
				[]compapiv1alpha1.ComponentVersion{{Name: "v1", Revision: "main"}, {Name: "v3", Revision: "feature"}},
				[]compapiv1alpha1.ComponentVersionStatus{{Name: "v1", Revision: "main"}, {Name: "v2", Revision: "develop"}},
			),
			wantVersionsToOnboard:  []string{"v3"},
			wantVersionsToOffboard: []string{"v2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ComponentBuildReconciler{}
			existingSpecVersions := buildVersionInfoMap(&tt.component, false)
			versionsToOnboard, versionsToOffboard := reconciler.determineVersionsToOnboardAndOffboard(
				context.TODO(), &tt.component, existingSpecVersions,
			)

			assert.Equal(t, len(tt.wantVersionsToOnboard), len(versionsToOnboard), "versions to onboard count mismatch")
			assert.Equal(t, len(tt.wantVersionsToOffboard), len(versionsToOffboard), "versions to offboard count mismatch")

			for _, v := range tt.wantVersionsToOnboard {
				assert.Assert(t, slices.Contains(versionsToOnboard, v), "expected version to onboard %s not found", v)
			}
			for _, v := range tt.wantVersionsToOffboard {
				assert.Assert(t, slices.Contains(versionsToOffboard, v), "expected version to offboard %s not found", v)
			}
		})
	}
}

func TestDetermineVersionsToTriggerBuild(t *testing.T) {
	getComponent := func(triggerVersion string, triggerVersions []string) compapiv1alpha1.Component {
		component := compapiv1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testcomponent",
				Namespace: "workspace-name",
			},
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitURL: "https://github.com/user/repo.git",
					},
				},
				Actions: compapiv1alpha1.ComponentActions{TriggerBuild: triggerVersion, TriggerBuilds: triggerVersions},
			},
		}
		return component
	}

	existingVersions := map[string]*VersionInfo{
		"v1": {OriginalVersion: "v1", SanitizedVersion: "v1", Revision: "main"},
		"v2": {OriginalVersion: "v2", SanitizedVersion: "v2", Revision: "develop"},
	}

	tests := []struct {
		name                                 string
		component                            compapiv1alpha1.Component
		versionsToOnboard                    []string
		versionsToCreatePRFor                []string
		wantVersionsToTriggerBuildFor        []string
		wantInvalidVersionsToTriggerBuildFor []string
	}{
		{
			name:                                 "should filter out versions being onboarded",
			component:                            getComponent("", []string{"v1", "v2"}),
			versionsToOnboard:                    []string{"v1"},
			versionsToCreatePRFor:                []string{},
			wantVersionsToTriggerBuildFor:        []string{"v2"},
			wantInvalidVersionsToTriggerBuildFor: []string{"v1"},
		},
		{
			name:                                 "should filter out versions being created with configuration",
			component:                            getComponent("", []string{"v1", "v2"}),
			versionsToOnboard:                    []string{},
			versionsToCreatePRFor:                []string{"v2"},
			wantVersionsToTriggerBuildFor:        []string{"v1"},
			wantInvalidVersionsToTriggerBuildFor: []string{"v2"},
		},
		{
			name:                                 "should filter out invalid versions",
			component:                            getComponent("", []string{"v1", "v3"}),
			versionsToOnboard:                    []string{},
			versionsToCreatePRFor:                []string{},
			wantVersionsToTriggerBuildFor:        []string{"v1"},
			wantInvalidVersionsToTriggerBuildFor: []string{"v3"},
		},
		{
			name:                                 "should filter out invalid versions and get versions from version and versions",
			component:                            getComponent("v2", []string{"v1", "v3"}),
			versionsToOnboard:                    []string{},
			versionsToCreatePRFor:                []string{},
			wantVersionsToTriggerBuildFor:        []string{"v1", "v2"},
			wantInvalidVersionsToTriggerBuildFor: []string{"v3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ComponentBuildReconciler{}
			versionsToTriggerBuildFor, invalidVersionsToTriggerBuildFor := reconciler.determineVersionsToTriggerBuildFor(
				ctx, &tt.component, existingVersions, tt.versionsToOnboard, tt.versionsToCreatePRFor,
			)

			assert.Equal(t, len(tt.wantVersionsToTriggerBuildFor), len(versionsToTriggerBuildFor), "versions to trigger build for count mismatch")
			assert.Equal(t, len(tt.wantInvalidVersionsToTriggerBuildFor), len(invalidVersionsToTriggerBuildFor), "invalid versions to trigger build for count mismatch")

			for _, v := range tt.wantVersionsToTriggerBuildFor {
				assert.Assert(t, slices.Contains(versionsToTriggerBuildFor, v), "expected version to trigger build for %s not found", v)
			}
			for _, v := range tt.wantInvalidVersionsToTriggerBuildFor {
				assert.Assert(t, slices.Contains(invalidVersionsToTriggerBuildFor, v), "expected invalid version to trigger build for %s not found", v)
			}
		})
	}
}
