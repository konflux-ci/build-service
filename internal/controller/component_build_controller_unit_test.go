/*
Copyright 2021-2025 Red Hat, Inc.

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
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/konflux-ci/build-service/pkg/bometrics"
	. "github.com/konflux-ci/build-service/pkg/common"
	pkgslices "github.com/konflux-ci/build-service/pkg/slices"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

const (
	ghAppPrivateKeyStub    = "-----BEGIN RSA PRIVATE KEY-----_key-content_-----END RSA PRIVATE KEY-----"
	testPipelineAnnotation = "{\"name\":\"pipeline_name\",\"bundle\":\"bundle_name\"}"
)

func TestGetProvisionTimeMetricsBuckets(t *testing.T) {
	buckets := bometrics.HistogramBuckets
	for i := 1; i < len(buckets); i++ {
		if buckets[i] <= buckets[i-1] {
			t.Errorf("Buckets must be in increasing order, but got: %v", buckets)
		}
	}
}

func TestGeneratePaCPipelineRunForComponent(t *testing.T) {
	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-component",
			Namespace: "my-namespace",
		},
		Spec: compapiv1alpha1.ComponentSpec{
			ContainerImage: "registry.io/username/image:tag",
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitURL: "https://githost.com/user/repo.git",
				},
			},
		},
	}
	versionInfo := &VersionInfo{Revision: "custom-branch", OriginalVersion: "version1", SanitizedVersion: "version1", Context: "./base_context", DockerfileURI: "containerFile"}
	param1_value := "param1_value"
	param2_value := []string{"param2_value1", "param2_value2"}
	pipelineSpec := &tektonapi.PipelineSpec{
		Workspaces: []tektonapi.PipelineWorkspaceDeclaration{
			{
				Name: "git-auth",
			},
			{
				Name: "workspace",
			},
		},
		Params: tektonapi.ParamSpecs{
			{Name: "add-param1", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: param1_value}},
			{Name: "add-param2", Type: "array", Default: &tektonapi.ParamValue{Type: "array", ArrayVal: param2_value}},
			{Name: "output-image", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "image-expires-after", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "dockerfile", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "path-context", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
		},
	}
	pipelineDefinition := &PipelineDef{AdditionalParams: []string{"add-param1", "add-param2", "non-existing"}}
	ResetTestGitProviderClient()

	pipelineRun, err := generatePaCPipelineRunForComponent(component, pipelineSpec, pipelineDefinition, versionInfo, testGitProviderClient, true)
	if err != nil {
		t.Error("generatePaCPipelineRunForComponent(): Failed to generate pipeline run")
	}

	if pipelineRun.Name != component.Name+"-"+versionInfo.SanitizedVersion+pipelineRunOnPRSuffix {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipeline name")
	}
	if pipelineRun.Namespace != "my-namespace" {
		t.Error("generatePaCPipelineRunForComponent(): pipeline namespace doesn't match")
	}

	if pipelineRun.Labels[ComponentNameLabelName] != "my-component" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s label value", ComponentNameLabelName)
	}
	if pipelineRun.Labels["pipelines.appstudio.openshift.io/type"] != "build" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelines.appstudio.openshift.io/type label value")
	}

	onCel := `event == "pull_request" && target_branch == "custom-branch" && ( "./base_context/***".pathChanged() || ".tekton/my-component-version1-pull-request.yaml".pathChanged() )`
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/on-cel-expression"] != onCel {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/on-cel-expression annotation value")
	}
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/max-keep-runs"] != "3" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/max-keep-runs annotation value")
	}
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/cancel-in-progress"] != "true" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/cancel-in-progress annotation value")
	}
	if pipelineRun.Annotations["build.appstudio.redhat.com/target_branch"] != "{{target_branch}}" {
		t.Error("generatePaCPipelineRunForComponent(): wrong build.appstudio.redhat.com/target_branch annotation value")
	}
	if pipelineRun.Annotations[gitCommitShaAnnotationName] != "{{revision}}" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s annotation value", gitCommitShaAnnotationName)
	}
	if pipelineRun.Annotations[gitRepoAtShaAnnotationName] != "https://githost.com/user/repo?rev={{revision}}" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s annotation value", gitRepoAtShaAnnotationName)
	}
	if pipelineRun.Annotations["build.appstudio.redhat.com/pull_request_number"] != "{{pull_request_number}}" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong build.appstudio.redhat.com/pull_request_number annotation value")
	}
	if pipelineRun.Annotations[VersionAnnotationName] != versionInfo.OriginalVersion {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s annotation value", VersionAnnotationName)
	}

	if len(pipelineRun.Spec.Params) != 8 {
		t.Error("generatePaCPipelineRunForComponent(): wrong number of pipeline params")
	}
	for _, param := range pipelineRun.Spec.Params {
		switch param.Name {
		case "git-url":
			if param.Value.StringVal != "{{source_url}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s", param.Name)
			}
		case "revision":
			if param.Value.StringVal != "{{revision}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "output-image":
			if !strings.HasPrefix(param.Value.StringVal, "registry.io/username/image:on-pr-{{revision}}") {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "image-expires-after":
			if param.Value.StringVal != "5d" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "dockerfile":
			if param.Value.StringVal != "containerFile" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "path-context":
			if param.Value.StringVal != "base_context" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param1":
			if param.Value.StringVal != param1_value {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param2":
			if len(param.Value.ArrayVal) != len(param2_value) {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
			for idx := range param2_value {
				if param2_value[idx] != param.Value.ArrayVal[idx] {
					t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)

				}
			}
		default:
			t.Errorf("generatePaCPipelineRunForComponent(): unexpected pipeline parameter %v", param)
		}
	}

	if len(pipelineRun.Spec.Workspaces) != 2 {
		t.Error("generatePaCPipelineRunForComponent(): wrong number of pipeline workspaces")
	}
	for _, workspace := range pipelineRun.Spec.Workspaces {
		if workspace.Name == "workspace" {
			continue
		}
		if workspace.Name == "git-auth" {
			continue
		}
		t.Errorf("generatePaCPipelineRunForComponent(): unexpected pipeline workspaces %v", workspace)
	}

	if pipelineRun.Spec.TaskRunTemplate.ServiceAccountName != "build-pipeline-"+component.Name {
		t.Error("generatePaCPipelineRunForComponent(): build pipeline service account is incorrect")
	}

	// test that if default params aren't in the spec, we won't add them
	pipelineSpec2 := &tektonapi.PipelineSpec{
		Params: tektonapi.ParamSpecs{
			{Name: "add-param1", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: param1_value}},
			{Name: "add-param2", Type: "array", Default: &tektonapi.ParamValue{Type: "array", ArrayVal: param2_value}},
			{Name: "output-image", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "image-expires-after", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
		},
	}
	pipelineRun2, err := generatePaCPipelineRunForComponent(component, pipelineSpec2, pipelineDefinition, versionInfo, testGitProviderClient, true)
	if err != nil {
		t.Error("generatePaCPipelineRunForComponent(): Failed to generate pipeline run 2")
	}

	for _, param := range pipelineRun2.Spec.Params {
		switch param.Name {
		case "git-url":
			if param.Value.StringVal != "{{source_url}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s", param.Name)
			}
		case "revision":
			if param.Value.StringVal != "{{revision}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "output-image":
			if !strings.HasPrefix(param.Value.StringVal, "registry.io/username/image:on-pr-{{revision}}") {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "image-expires-after":
			if param.Value.StringVal != "5d" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param1":
			if param.Value.StringVal != param1_value {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param2":
			if len(param.Value.ArrayVal) != len(param2_value) {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
			for idx := range param2_value {
				if param2_value[idx] != param.Value.ArrayVal[idx] {
					t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)

				}
			}
		default:
			t.Errorf("generatePaCPipelineRunForComponent(): unexpected pipeline parameter %v", param)
		}
	}
}

func TestGeneratePaCPipelineRunForComponent_ShouldStopIfTargetBranchIsNotSet(t *testing.T) {
	_, err := generatePaCPipelineRunForComponent(&compapiv1alpha1.Component{ObjectMeta: metav1.ObjectMeta{Name: "my-component", Namespace: "my-namespace"}},
		nil, nil, &VersionInfo{}, nil, true)
	if err == nil {
		t.Errorf("generatePaCPipelineRunForComponent(): expected error")
	}
}

func TestGenerateCelExpressionForPipeline(t *testing.T) {
	componentKey := types.NamespacedName{Namespace: "test-ns", Name: "comp1"}
	ResetTestGitProviderClient()

	tests := []struct {
		name              string
		isDockerfileExist func(repoUrl, branch, dockerfilePath string) (bool, error)
		wantOnPullError   bool
		wantOnPushError   bool
		wantOnPull        string
		wantOnPush        string
		versionInfo       *VersionInfo
	}{
		{
			name:        "should generate cel expression for component that occupies whole git repository",
			wantOnPull:  `event == "pull_request" && target_branch == "my-branch"`,
			wantOnPush:  `event == "push" && target_branch == "my-branch"`,
			versionInfo: &VersionInfo{Revision: "my-branch", SanitizedVersion: "version1"},
		},
		{
			name:        "should generate cel expression for component with context directory, without dokerfile",
			wantOnPull:  `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-pull-request.yaml".pathChanged() )`,
			wantOnPush:  `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-push.yaml".pathChanged() )`,
			versionInfo: &VersionInfo{Revision: "my-branch", Context: "component-dir", SanitizedVersion: "version1"},
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile in context directory",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return true, nil
			},
			wantOnPull:  `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-pull-request.yaml".pathChanged() )`,
			wantOnPush:  `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-push.yaml".pathChanged() )`,
			versionInfo: &VersionInfo{Revision: "my-branch", Context: "component-dir", DockerfileURI: "dockerfile/Dockerfile", SanitizedVersion: "version1"},
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile outside context directory",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, nil
			},
			wantOnPull:  `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-pull-request.yaml".pathChanged() || "docker-root-dir/Dockerfile".pathChanged() )`,
			wantOnPush:  `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-push.yaml".pathChanged() || "docker-root-dir/Dockerfile".pathChanged() )`,
			versionInfo: &VersionInfo{Revision: "my-branch", Context: "component-dir", DockerfileURI: "docker-root-dir/Dockerfile", SanitizedVersion: "version1"},
		},
		{
			name: "should fail to generate cel expression for component if isFileExist fails",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, fmt.Errorf("Failed to check file existance")
			},
			wantOnPullError: true,
			wantOnPushError: true,
			versionInfo:     &VersionInfo{Revision: "my-branch", Context: "component-dir", DockerfileURI: "non-existing/Dockerfile", SanitizedVersion: "version1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.isDockerfileExist != nil {
				IsFileExistFunc = tt.isDockerfileExist
			} else {
				IsFileExistFunc = func(repoUrl, branchName, filePath string) (bool, error) {
					t.Errorf("IsFileExist should not be invoked")
					return false, nil
				}
			}
			component := getSampleComponentData(componentKey)
			got, err := generateCelExpressionForPipeline(component, testGitProviderClient, tt.versionInfo, true)
			if err != nil {
				if !tt.wantOnPullError {
					t.Errorf("generateCelExpressionForPipeline(on pull): got err: %v", err)
				}
			} else {
				if got != tt.wantOnPull {
					t.Errorf("generateCelExpressionForPipeline(on pull): got '%s', want '%s'", got, tt.wantOnPull)
				}
			}

			got, err = generateCelExpressionForPipeline(component, testGitProviderClient, tt.versionInfo, false)
			if err != nil {
				if !tt.wantOnPushError {
					t.Errorf("generateCelExpressionForPipeline(on push): got err: %v", err)
				}
			}
			if got != tt.wantOnPush {
				t.Errorf("generateCelExpressionForPipeline(on push): got '%s', want '%s'", got, tt.wantOnPush)
			}
		})
	}
	ResetTestGitProviderClient()
}

func TestGetContainerImageRepository(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  string
	}{
		{
			name:  "should not change image",
			image: "image-name",
			want:  "image-name",
		},
		{
			name:  "should not change /user/image",
			image: "user/image",
			want:  "user/image",
		},
		{
			name:  "should not change repository.io/user/image",
			image: "repository.io/user/image",
			want:  "repository.io/user/image",
		},
		{
			name:  "should delete tag",
			image: "repository.io/user/image:tag",
			want:  "repository.io/user/image",
		},
		{
			name:  "should delete sha",
			image: "repository.io/user/image@sha256:586ab46b9d6d906b2df3dad12751e807bd0f0632d5a2ab3991bdac78bdccd59a",
			want:  "repository.io/user/image",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getContainerImageRepository(tt.image)
			if got != tt.want {
				t.Errorf("getContainerImageRepository(): got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidatePaCConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		gitProvider string
		secret      corev1.Secret
		expectError bool
	}{
		{
			name:        "should accept GitHub application configuration",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
				},
			},
			expectError: false,
		},
		{
			name:        "should accept GitHub application configuration with end line",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub + "\n"),
				},
			},
			expectError: false,
		},
		{
			name:        "should accept GitHub token configuration",
			gitProvider: "github",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"password": []byte(base64.StdEncoding.EncodeToString([]byte("ghp_token"))),
				},
			},
			expectError: false,
		},
		{
			name:        "should accept GitHub basic auth configuration",
			gitProvider: "github",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"username": []byte(base64.StdEncoding.EncodeToString([]byte("user"))),
					"password": []byte(base64.StdEncoding.EncodeToString([]byte("password"))),
				},
			},
			expectError: false,
		},
		{
			name:        "should reject empty GitHub access token",
			gitProvider: "github",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"password": []byte(""),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject empty GitHub password",
			gitProvider: "github",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"username": []byte(base64.StdEncoding.EncodeToString([]byte("user"))),
				},
			},
			expectError: true,
		},
		{
			name:        "should accept GitHub application configuration if both GitHub application and webhook configured",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
					"password":                      []byte("ghp_token"),
				},
			},
			expectError: false,
		},
		{
			name:        "should reject GitHub application configuration if GitHub id is missing",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
					"password":                      []byte("ghp_token"),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject GitHub application configuration if GitHub application private key is missing",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey: []byte("12345"),
					"password":                    []byte("ghp_token"),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject GitHub application configuration if GitHub application id is invalid",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12ab"),
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject GitHub application configuration if GitHub application application private key is invalid",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				},
			},
			expectError: true,
		},
		{
			name:        "should accept GitLab webhook configuration",
			gitProvider: "gitlab",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"password": []byte(base64.StdEncoding.EncodeToString([]byte("token"))),
				},
			},
			expectError: false,
		},
		{
			name:        "should accept GitLab basic auth configuration",
			gitProvider: "gitlab",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"username": []byte(base64.StdEncoding.EncodeToString([]byte("user"))),
					"password": []byte(base64.StdEncoding.EncodeToString([]byte("password"))),
				},
			},
			expectError: false,
		},
		{
			name:        "should reject empty GitLab webhook token",
			gitProvider: "gitlab",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"password": []byte(""),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject unknown application configuration",
			gitProvider: "unknown",
			secret: corev1.Secret{
				Data: map[string][]byte{},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePaCConfiguration(tt.gitProvider, tt.secret)
			if err != nil {
				if !tt.expectError {
					t.Errorf("Expected that the configuration %#v from provider %s should be valid", tt.secret.Data, tt.gitProvider)
				}
			} else {
				if tt.expectError {
					t.Errorf("Expected that the configuration %#v from provider %s fails", tt.secret.Data, tt.gitProvider)
				}
			}
		})
	}
}

func TestGeneratePaCWebhookSecretString(t *testing.T) {
	expectedSecretStringLength := 20 * 2 // each byte is represented by 2 hex chars

	t.Run("should be able to generate webhook secret string", func(t *testing.T) {
		secret := generatePaCWebhookSecretString()
		if len(secret) != expectedSecretStringLength {
			t.Errorf("Expected that webhook secret string has length %d, but got %d", expectedSecretStringLength, len(secret))
		}
	})

	t.Run("should generate different webhook secret strings", func(t *testing.T) {
		n := 100
		secrets := make([]string, n)
		for i := 0; i < n; i++ {
			secrets[i] = generatePaCWebhookSecretString()
		}

		secret := secrets[0]
		for i := 1; i < n; i++ {
			if secret == secrets[i] {
				t.Errorf("All webhook secrets strings must be different")
			}
		}
	})
}

func TestGetPathContext(t *testing.T) {
	tests := []struct {
		name              string
		gitContext        string
		dockerfileContext string
		want              string
	}{
		{
			name:              "should return empty context if both contexts empty",
			gitContext:        "",
			dockerfileContext: "",
			want:              "",
		},
		{
			name:              "should use current directory from git context",
			gitContext:        ".",
			dockerfileContext: "",
			want:              ".",
		},
		{
			name:              "should use current directory from dockerfile context",
			gitContext:        "",
			dockerfileContext: ".",
			want:              ".",
		},
		{
			name:              "should use current directory if both contexts are current directory",
			gitContext:        ".",
			dockerfileContext: ".",
			want:              ".",
		},
		{
			name:              "should use git context if dockerfile context if not set",
			gitContext:        "dir",
			dockerfileContext: "",
			want:              "dir",
		},
		{
			name:              "should use dockerfile context if git context if not set",
			gitContext:        "",
			dockerfileContext: "dir",
			want:              "dir",
		},
		{
			name:              "should use git context if dockerfile context is current directory",
			gitContext:        "dir",
			dockerfileContext: ".",
			want:              "dir",
		},
		{
			name:              "should use dockerfile context if git context is current directory",
			gitContext:        ".",
			dockerfileContext: "dir",
			want:              "dir",
		},
		{
			name:              "should respect both git and dockerfile contexts",
			gitContext:        "component-dir",
			dockerfileContext: "dockerfile-dir",
			want:              "component-dir/dockerfile-dir",
		},
		{
			name:              "should respect both git and dockerfile contexts in subfolders",
			gitContext:        "path/to/component",
			dockerfileContext: "path/to/dockercontext/",
			want:              "path/to/component/path/to/dockercontext",
		},
		{
			name:              "should remove slash at the end",
			gitContext:        "path/to/dir/",
			dockerfileContext: "",
			want:              "path/to/dir",
		},
		{
			name:              "should remove slash at the end",
			gitContext:        "",
			dockerfileContext: "path/to/dir/",
			want:              "path/to/dir",
		},
		{
			name:              "should not allow absolute path",
			gitContext:        "/path/to/dir/",
			dockerfileContext: "",
			want:              "path/to/dir",
		},
		{
			name:              "should not allow absolute path",
			gitContext:        "",
			dockerfileContext: "/path/to/dir/",
			want:              "path/to/dir",
		},
		{
			name:              "should not allow absolute path",
			gitContext:        "/path/to/dir1/",
			dockerfileContext: "/path/to/dir2/",
			want:              "path/to/dir1/path/to/dir2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getPathContext(tt.gitContext, tt.dockerfileContext)
			if got != tt.want {
				t.Errorf("Expected \"%s\", but got \"%s\"", tt.want, got)
			}
		})
	}
}

func TestCreateWorkspaceBinding(t *testing.T) {
	tests := []struct {
		name                      string
		pipelineWorkspaces        []tektonapi.PipelineWorkspaceDeclaration
		expectedWorkspaceBindings []tektonapi.WorkspaceBinding
	}{
		{
			name: "should not bind unknown workspaces",
			pipelineWorkspaces: []tektonapi.PipelineWorkspaceDeclaration{
				{
					Name: "unknown1",
				},
				{
					Name: "unknown2",
				},
			},
			expectedWorkspaceBindings: []tektonapi.WorkspaceBinding{},
		},
		{
			name: "should bind git-auth",
			pipelineWorkspaces: []tektonapi.PipelineWorkspaceDeclaration{
				{
					Name: "git-auth",
				},
			},
			expectedWorkspaceBindings: []tektonapi.WorkspaceBinding{
				{
					Name:   "git-auth",
					Secret: &corev1.SecretVolumeSource{SecretName: "{{ git_auth_secret }}"},
				},
			},
		},
		{
			name: "should bind git-auth and workspace, should not bind unknown",
			pipelineWorkspaces: []tektonapi.PipelineWorkspaceDeclaration{
				{
					Name: "git-auth",
				},
				{
					Name: "unknown",
				},
				{
					Name: "workspace",
				},
			},
			expectedWorkspaceBindings: []tektonapi.WorkspaceBinding{
				{
					Name:   "git-auth",
					Secret: &corev1.SecretVolumeSource{SecretName: "{{ git_auth_secret }}"},
				},
				{
					Name:                "workspace",
					VolumeClaimTemplate: generateVolumeClaimTemplate(),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := createWorkspaceBinding(tt.pipelineWorkspaces)
			if !reflect.DeepEqual(got, tt.expectedWorkspaceBindings) {
				t.Errorf("Expected %#v, but received %#v", tt.expectedWorkspaceBindings, got)
			}
		})
	}
}

func TestSlicesIntersection(t *testing.T) {
	tests := []struct {
		in1, in2     []string
		intersection int
	}{
		{
			in1:          []string{"a", "b", "c", "d", "e"},
			in2:          []string{"a", "b", "c", "d", "e"},
			intersection: 5,
		},
		{
			in1:          []string{"a", "b", "c", "d", "e"},
			in2:          []string{"a", "b", "c", "q", "y"},
			intersection: 3,
		},
		{
			in1:          []string{"a", "b", "c", "d", "e"},
			in2:          []string{"a", "q", "c", "f", "y"},
			intersection: 1,
		},
		{
			in1:          []string{"a", "b", "c", "d", "e"},
			in2:          []string{"f", "b", "c", "d", "e"},
			intersection: 0,
		},
	}
	for _, tt := range tests {
		t.Run("intersection test", func(t *testing.T) {
			got := pkgslices.Intersection(tt.in1, tt.in2)
			if got != tt.intersection {
				t.Errorf("Got slice intersection %d but expected length is %d", got, tt.intersection)
			}
		})
	}
}

func TestGeneratePACRepository(t *testing.T) {
	getComponent := func(repoUrl string, annotations map[string]string, repoSettings *compapiv1alpha1.RepositorySettings) compapiv1alpha1.Component {
		component := compapiv1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "testcomponent",
				Namespace:   "workspace-name",
				Annotations: annotations,
			},
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitURL: repoUrl,
					},
				},
			},
		}
		if repoSettings != nil {
			component.Spec.RepositorySettings = *repoSettings
		}
		return component
	}

	tests := []struct {
		name                             string
		repoUrl                          string
		componentAnnotations             map[string]string
		pacConfig                        map[string][]byte
		expectedGitProviderConfig        *pacv1alpha1.GitProvider
		repoSettings                     *compapiv1alpha1.RepositorySettings
		expectedGithubAppTokenScopeRepos *[]string
		expectedCommentStrategy          string
	}{
		{
			name:    "should create PaC repository for GitHub application",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub application even if Github webhook configured",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("ghp_token"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub webhook",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.com/user/test-component-repository", nil, nil), true),
				},
				URL:  "",
				Type: "github",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub application on self-hosted GitHub",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
				GitProviderAnnotationURL:  "https://github.self-hosted.com",
			},
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitHub webhook and figure out provider URL from source URL",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
			},
			pacConfig: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://github.self-hosted.com",
				Type: "github",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitHub webhook and use provider URL from annotation",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
				GitProviderAnnotationURL:  "https://github.self-hosted-proxy.com",
			},
			pacConfig: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://github.self-hosted-proxy.com",
				Type: "github",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitLab webhook",
			repoUrl: "https://gitlab.com/user/test-component-repository/",
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "",
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitLab webhook even if GitHub application configured",
			repoUrl: "https://gitlab.com/user/test-component-repository.git",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository", nil, nil), true),
				},
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitLab webhook and figure out provider URL from source URL",
			repoUrl: "https://gitlab.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "gitlab",
			},
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://gitlab.self-hosted.com",
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitLab webhook and use provider URL from annotation",
			repoUrl: "https://gitlab.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "gitlab",
				GitProviderAnnotationURL:  "https://gitlab.self-hosted-proxy.com",
			},
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://gitlab.self-hosted-proxy.com",
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitLab webhook and use provider URL from annotation that has no protocol",
			repoUrl: "https://gitlab.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "gitlab",
				GitProviderAnnotationURL:  "gitlab.self-hosted-proxy.com",
			},
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://gitlab.self-hosted-proxy.com",
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for Forgejo webhook with gitea type for PaC compatibility",
			repoUrl: "https://forgejo.example.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "forgejo",
			},
			pacConfig: map[string][]byte{
				"password": []byte("forgejo-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://forgejo.example.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://forgejo.example.com",
				Type: "gitea",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub application with repo settings CommentStrategy",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     &compapiv1alpha1.RepositorySettings{CommentStrategy: "disable_all"},
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "disable_all",
		},
		{
			name:    "should create PaC repository for GitHub application with repo settings GithubAppTokenScopeRepos",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     &compapiv1alpha1.RepositorySettings{GithubAppTokenScopeRepos: []string{"scope1", "scope2"}},
			expectedGithubAppTokenScopeRepos: &[]string{"scope1", "scope2"},
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub application with repo settings CommentStrategy and GithubAppTokenScopeRepos",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig: nil,
			repoSettings: &compapiv1alpha1.RepositorySettings{
				CommentStrategy:          "disable_all",
				GithubAppTokenScopeRepos: []string{"scope1", "scope2"}},
			expectedGithubAppTokenScopeRepos: &[]string{"scope1", "scope2"},
			expectedCommentStrategy:          "disable_all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := getComponent(tt.repoUrl, tt.componentAnnotations, tt.repoSettings)
			secret := &corev1.Secret{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Secret",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelinesAsCodeGitHubAppSecretName,
					Namespace: component.Namespace,
				},
				Data: tt.pacConfig,
			}
			pacRepo, err := generatePACRepository(component, secret, &component.Spec.RepositorySettings, true)

			if err != nil {
				t.Errorf("Failed to generate PaC repository object. Cause: %v", err)
			}

			expectedRepo := strings.TrimSuffix(strings.TrimSuffix(tt.repoUrl, ".git"), "/")
			repositoryName, _ := generatePaCRepositoryNameFromGitUrl(expectedRepo)
			if pacRepo.Name != repositoryName {
				t.Errorf("Generated PaC repository must have name based on component's git url")
			}
			if pacRepo.Namespace != component.Namespace {
				t.Errorf("Generated PaC repository must have the same namespace as corresponding component")
			}

			if pacRepo.Spec.URL != expectedRepo {
				t.Errorf("Wrong git repository URL in PaC repository: %s, want %s", pacRepo.Spec.URL, expectedRepo)
			}
			if !reflect.DeepEqual(pacRepo.Spec.GitProvider, tt.expectedGitProviderConfig) {
				t.Errorf("Wrong git provider config in PaC repository: %#v, want %#v", pacRepo.Spec.GitProvider, tt.expectedGitProviderConfig)
			}

			if tt.expectedGithubAppTokenScopeRepos == nil {
				if len(pacRepo.Spec.Settings.GithubAppTokenScopeRepos) != 0 {
					t.Errorf("Wrong settings GithubAppTokenScopeRepos in PaC repository: %#v, want %#v", pacRepo.Spec.Settings.GithubAppTokenScopeRepos, []string{})
				}
			} else if !reflect.DeepEqual(pacRepo.Spec.Settings.GithubAppTokenScopeRepos, *tt.expectedGithubAppTokenScopeRepos) {
				t.Errorf("Wrong settings GithubAppTokenScopeRepos in PaC repository: %#v, want %#v", pacRepo.Spec.Settings.GithubAppTokenScopeRepos, *tt.expectedGithubAppTokenScopeRepos)
			}

			if pacRepo.Spec.Settings.Github.CommentStrategy != tt.expectedCommentStrategy {
				t.Errorf("Wrong settings Github.CommentStrategy in PaC repository: %s, want %s", pacRepo.Spec.Settings.Github.CommentStrategy, tt.expectedCommentStrategy)
			}
			if pacRepo.Spec.Settings.Gitlab.CommentStrategy != tt.expectedCommentStrategy {
				t.Errorf("Wrong settings Gitlab.CommentStrategy in PaC repository: %s, want %s", pacRepo.Spec.Settings.Gitlab.CommentStrategy, tt.expectedCommentStrategy)
			}
		})
	}
}

func TestPaCRepoAddParamWorkspace(t *testing.T) {
	const workspaceName = "someone-tenant"

	pacConfig := map[string][]byte{
		PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
		PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
	}

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-secret",
			Namespace: workspaceName,
		},
		Data: pacConfig,
	}

	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-component",
			Namespace: "my-namespace",
		},
		Spec: compapiv1alpha1.ComponentSpec{
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitURL: "https://github.com/user/repo.git",
				},
			},
		},
	}

	convertCustomParamsToMap := func(repository *pacv1alpha1.Repository) map[string]pacv1alpha1.Params {
		result := map[string]pacv1alpha1.Params{}
		for _, param := range *repository.Spec.Params {
			result[param.Name] = param
		}
		return result
	}

	t.Run("add to Spec.Params", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret, nil, true)
		pacRepoAddParamWorkspaceName(repository, workspaceName)

		params := convertCustomParamsToMap(repository)
		param, ok := params[pacCustomParamAppstudioWorkspace]
		assert.Assert(t, ok)
		assert.Equal(t, workspaceName, param.Value)
	})

	t.Run("override existing workspace parameter, unset other fields btw", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret, nil, true)
		params := []pacv1alpha1.Params{
			{
				Name:      pacCustomParamAppstudioWorkspace,
				Value:     "another_workspace",
				Filter:    "pac.event_type == \"pull_request\"",
				SecretRef: &pacv1alpha1.Secret{},
			},
		}
		repository.Spec.Params = &params

		pacRepoAddParamWorkspaceName(repository, workspaceName)

		existingParams := convertCustomParamsToMap(repository)
		param, ok := existingParams[pacCustomParamAppstudioWorkspace]
		assert.Assert(t, ok)
		assert.Equal(t, workspaceName, param.Value)

		assert.Equal(t, "", param.Filter)
		assert.Assert(t, param.SecretRef == nil)
	})
}

func TestGetGitProvider(t *testing.T) {
	getComponent := func(repoUrl, annotationValue string) compapiv1alpha1.Component {
		component := compapiv1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testcomponent",
				Namespace: "workspace-name",
			},
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitURL: repoUrl,
					},
				},
			},
		}
		if annotationValue != "" {
			component.ObjectMeta.Annotations = map[string]string{
				GitProviderAnnotationName: annotationValue,
			}
		}
		return component
	}

	tests := []struct {
		name                           string
		componentRepoUrl               string
		componentGitProviderAnnotation string
		want                           string
		expectError                    bool
	}{
		{
			name:             "should detect github provider via https url",
			componentRepoUrl: "https://github.com/user/test-component-repository",
			want:             "github",
		},
		{
			name:             "should detect github provider via git url",
			componentRepoUrl: "git@github.com:user/test-component-repository",
			expectError:      true,
			want:             "github",
		},
		{
			name:             "should detect non-standard github provider via https url",
			componentRepoUrl: "https://cooler.github.my-company.com/user/test-component-repository",
			want:             "github",
		},
		{
			name:             "should detect non-standard github provider via git url",
			componentRepoUrl: "git@cooler.github.my-company.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:             "should detect gitlab provider via https url",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider via git url",
			componentRepoUrl: "git@gitlab.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:             "should detect non-standard gitlab provider via https url",
			componentRepoUrl: "https://cooler.gitlab.my-company.com/user/test-component-repository",
			want:             "gitlab",
		},
		{
			name:             "should detect non-standard gitlab provider via git url",
			componentRepoUrl: "git@cooler.gitlab.my-company.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:                           "should detect github provider via annotation",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "github",
			want:                           "github",
		},
		{
			name:                           "should detect gitlab provider via annotation",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "gitlab",
			want:                           "gitlab",
		},
		{
			name:                           "should prefer the annotation over the url",
			componentRepoUrl:               "https://not.github.my-company.com/user/test-component-repository",
			componentGitProviderAnnotation: "gitlab",
			want:                           "gitlab",
		},
		{
			name:             "should fail to detect git provider for self-hosted instance if annotation is not set",
			componentRepoUrl: "https://mydomain.com/user/test-component-repository",
			expectError:      true,
		},
		{
			name:                           "should fail to detect git provider for self-hosted instance if annotation is set to invalid value",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "mylab",
			expectError:                    true,
		},
		{
			name:             "should fail to detect git provider component repository URL is invalid",
			componentRepoUrl: "12345",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL is empty",
			componentRepoUrl: "",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path doesn't have 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path doesn't have 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path has more than 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user/repository/tree",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path has more than 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user/repository/tree/branch/file",
			expectError:      true,
		},
		{
			name:             "should detect gitlab provider even if path has more than 2 parts",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider even if path has more than 2 parts",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider url ends with '.git'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository.git",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider url ends with '.git' and slash",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository.git/",
			want:             "gitlab",
		},
		{
			name:             "should return error if gitlab url contains '-'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other/-/tree/main/file",
			expectError:      true,
		},
		{
			name:             "should return error if gitlab url contains '-'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other/-/commit/shacommit",
			expectError:      true,
		},
		{
			name:             "should return error if gitlab url contains '-'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other/-/blob/main/blobfile",
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := getComponent(tt.componentRepoUrl, tt.componentGitProviderAnnotation)
			got, err := getGitProvider(component, true)
			if tt.expectError {
				if err == nil {
					t.Errorf("Detecting git provider for component with '%s' url and '%s' annotation value should fail", tt.componentRepoUrl, tt.componentGitProviderAnnotation)
				}
			} else {
				if got != tt.want {
					t.Errorf("Expected git provider is: %s, but got %s", tt.want, got)
				}
			}
		})
	}

	t.Run("should return error if git source is nil", func(t *testing.T) {
		component := getComponent("", "")
		component.Spec.Source.GitURL = ""
		_, err := getGitProvider(component, true)
		if err == nil {
			t.Error("Expected error for nil source URL")
		}
	})
}
