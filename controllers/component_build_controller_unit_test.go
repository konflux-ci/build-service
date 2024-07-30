/*
Copyright 2021-2022 Red Hat, Inc.

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
	"github.com/konflux-ci/build-service/pkg/slices"

	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
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

func TestReadBuildStatus(t *testing.T) {
	tests := []struct {
		name                       string
		buildStatusAnnotationValue string
		want                       *BuildStatus
	}{
		{
			name:                       "should be able to read build status with all fields",
			buildStatusAnnotationValue: "{\"simple\":{\"build-start-time\":\"time\",\"error-id\":1,\"error-message\":\"simple-build-error\"},\"pac\":{\"state\":\"enabled\",\"configuration-time\":\"time\",\"error-id\":5,\"error-message\":\"pac-error\"},\"message\":\"done\"}",
			want: &BuildStatus{
				Simple: &SimpleBuildStatus{
					BuildStartTime: "time",
					ErrorInfo: ErrorInfo{
						ErrId:      1,
						ErrMessage: "simple-build-error",
					},
				},
				PaC: &PaCBuildStatus{
					State:             "enabled",
					ConfigurationTime: "time",
					ErrorInfo: ErrorInfo{
						ErrId:      5,
						ErrMessage: "pac-error",
					},
				},
				Message: "done",
			},
		},
		{
			name:                       "should return empty build status if annotation is empty or not set",
			buildStatusAnnotationValue: "",
			want:                       &BuildStatus{},
		},
		{
			name:                       "should return empty build status if curent one is not valid JSON",
			buildStatusAnnotationValue: "{\"simple\":{\"build-start-time\":\"time\"}",
			want:                       &BuildStatus{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := &appstudiov1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-component",
					Namespace: "my-namespace",
					Annotations: map[string]string{
						BuildStatusAnnotationName: tt.buildStatusAnnotationValue,
					},
				},
			}
			got := readBuildStatus(component)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("readBuildStatus(): actual: %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteBuildStatus(t *testing.T) {
	tests := []struct {
		name        string
		component   *appstudiov1alpha1.Component
		buildStatus *BuildStatus
		want        string
	}{
		{
			name: "should be able to write build status with all fields",
			component: &appstudiov1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-component",
					Namespace:   "my-namespace",
					Annotations: map[string]string{},
				},
			},
			buildStatus: &BuildStatus{
				Simple: &SimpleBuildStatus{
					BuildStartTime: "time",
					ErrorInfo: ErrorInfo{
						ErrId:      1,
						ErrMessage: "simple-build-error",
					},
				},
				PaC: &PaCBuildStatus{
					State: "enabled",
					ErrorInfo: ErrorInfo{
						ErrId:      5,
						ErrMessage: "pac-error",
					},
					ConfigurationTime: "time",
				},
				Message: "done",
			},
			want: "{\"simple\":{\"build-start-time\":\"time\",\"error-id\":1,\"error-message\":\"simple-build-error\"},\"pac\":{\"state\":\"enabled\",\"configuration-time\":\"time\",\"error-id\":5,\"error-message\":\"pac-error\"},\"message\":\"done\"}",
		},
		{
			name: "should be able to write build status when annotations is nil",
			component: &appstudiov1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-component",
					Namespace: "my-namespace",
				},
			},
			buildStatus: &BuildStatus{
				Simple: &SimpleBuildStatus{
					BuildStartTime: "time",
				},
				Message: "done",
			},
			want: "{\"simple\":{\"build-start-time\":\"time\"},\"message\":\"done\"}",
		},
		{
			name: "should be able to overwrite build status",
			component: &appstudiov1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-component",
					Namespace: "my-namespace",
					Annotations: map[string]string{
						BuildStatusAnnotationName: "{\"simple\":{\"build-start-time\":\"time\"},\"message\":\"done\"}",
					},
				},
			},
			buildStatus: &BuildStatus{
				PaC: &PaCBuildStatus{
					State: "error",
					ErrorInfo: ErrorInfo{
						ErrId:      10,
						ErrMessage: "error-ion-pac",
					},
					ConfigurationTime: "time",
				},
				Message: "done",
			},
			want: "{\"pac\":{\"state\":\"error\",\"configuration-time\":\"time\",\"error-id\":10,\"error-message\":\"error-ion-pac\"},\"message\":\"done\"}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeBuildStatus(tt.component, tt.buildStatus)
			got := tt.component.Annotations[BuildStatusAnnotationName]
			if got != tt.want {
				t.Errorf("writeBuildStatus(): actual: %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGeneratePaCPipelineRunForComponent(t *testing.T) {
	component := &appstudiov1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-component",
			Namespace: "my-namespace",
			Annotations: map[string]string{
				"skip-initial-checks":          "true",
				GitProviderAnnotationName:      "github",
				defaultBuildPipelineAnnotation: testPipelineAnnotation,
			},
		},
		Spec: appstudiov1alpha1.ComponentSpec{
			Application:    "my-application",
			ContainerImage: "registry.io/username/image:tag",
			Source: appstudiov1alpha1.ComponentSource{
				ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
					GitSource: &appstudiov1alpha1.GitSource{
						URL:           "https://githost.com/user/repo.git",
						Context:       "./base_context",
						DockerfileURL: "containerFile",
					},
				},
			},
		},
		Status: appstudiov1alpha1.ComponentStatus{},
	}
	pipelineSpec := &tektonapi.PipelineSpec{
		Workspaces: []tektonapi.PipelineWorkspaceDeclaration{
			{
				Name: "git-auth",
			},
			{
				Name: "workspace",
			},
		},
	}
	branchName := "custom-branch"
	ResetTestGitProviderClient()

	pipelineRun, err := generatePaCPipelineRunForComponent(component, pipelineSpec, branchName, testGitProviderClient, true)
	if err != nil {
		t.Error("generatePaCPipelineRunForComponent(): Failed to genertate pipeline run")
	}

	if pipelineRun.Name != component.Name+pipelineRunOnPRSuffix {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipeline name")
	}
	if pipelineRun.Namespace != "my-namespace" {
		t.Error("generatePaCPipelineRunForComponent(): pipeline namespace doesn't match")
	}

	if pipelineRun.Labels[ApplicationNameLabelName] != "my-application" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s label value", ApplicationNameLabelName)
	}
	if pipelineRun.Labels[ComponentNameLabelName] != "my-component" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s label value", ComponentNameLabelName)
	}
	if pipelineRun.Labels["pipelines.appstudio.openshift.io/type"] != "build" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelines.appstudio.openshift.io/type label value")
	}

	onCel := `event == "pull_request" && target_branch == "custom-branch" && ( "./base_context/***".pathChanged() || ".tekton/my-component-pull-request.yaml".pathChanged() )`
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/on-cel-expression"] != onCel {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/on-cel-expression annotation value")
	}
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/max-keep-runs"] != "3" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/max-keep-runs annotation value")
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

	if len(pipelineRun.Spec.Params) != 6 {
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
}

func TestGeneratePaCPipelineRunForComponent_ShouldStopIfTargetBranchIsNotSet(t *testing.T) {
	_, err := generatePaCPipelineRunForComponent(nil, nil, "", nil, true)
	if err == nil {
		t.Errorf("generatePaCPipelineRunForComponent(): expected error")
	}
}

func TestGenerateCelExpressionForPipeline(t *testing.T) {
	componentKey := types.NamespacedName{Namespace: "test-ns", Name: "component-name"}
	ResetTestGitProviderClient()

	tests := []struct {
		name              string
		component         *appstudiov1alpha1.Component
		targetBranch      string
		isDockerfileExist func(repoUrl, branch, dockerfilePath string) (bool, error)
		wantOnPullError   bool
		wantOnPull        string
		wantOnPush        string
	}{
		{
			name: "should generate cel expression for component that occupies whole git repository",
			component: func() *appstudiov1alpha1.Component {
				component := getSampleComponentData(componentKey)
				return component
			}(),
			targetBranch: "my-branch",
			wantOnPull:   `event == "pull_request" && target_branch == "my-branch"`,
			wantOnPush:   `event == "push" && target_branch == "my-branch"`,
		},
		{
			name: "should generate cel expression for component with context directory, without dokerfile",
			component: func() *appstudiov1alpha1.Component {
				component := getComponentData(componentConfig{componentKey: componentKey, gitSourceContext: "component-dir"})
				return component
			}(),
			targetBranch: "my-branch",
			wantOnPull:   `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() )`,
			wantOnPush:   `event == "push" && target_branch == "my-branch"`,
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile in context directory",
			component: func() *appstudiov1alpha1.Component {
				component := getComponentData(componentConfig{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "dockerfile/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return true, nil
			},
			wantOnPull: `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() )`,
			wantOnPush: `event == "push" && target_branch == "my-branch"`,
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile outside context directory",
			component: func() *appstudiov1alpha1.Component {
				component := getComponentData(componentConfig{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "docker-root-dir/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, nil
			},
			wantOnPull: `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() || "docker-root-dir/Dockerfile".pathChanged() )`,
			wantOnPush: `event == "push" && target_branch == "my-branch"`,
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile outside git repository",
			component: func() *appstudiov1alpha1.Component {
				component := getComponentData(componentConfig{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "https://host.com:1234/files/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			wantOnPull:   `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() )`,
			wantOnPush:   `event == "push" && target_branch == "my-branch"`,
		},
		{
			name: "should fail to generate cel expression for component if isFileExist fails",
			component: func() *appstudiov1alpha1.Component {
				component := getComponentData(componentConfig{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "non-existing/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, fmt.Errorf("Failed to check file existance")
			},
			wantOnPullError: true,
			wantOnPush:      `event == "push" && target_branch == "my-branch"`,
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

			got, err := generateCelExpressionForPipeline(tt.component, testGitProviderClient, tt.targetBranch, true)
			if err != nil {
				if !tt.wantOnPullError {
					t.Errorf("generateCelExpressionForPipeline(on pull): got err: %v", err)
				}
			} else {
				if got != tt.wantOnPull {
					t.Errorf("generateCelExpressionForPipeline(on pull): got '%s', want '%s'", got, tt.wantOnPull)
				}
			}

			got, err = generateCelExpressionForPipeline(tt.component, testGitProviderClient, tt.targetBranch, false)
			if err != nil {
				t.Errorf("generateCelExpressionForPipeline(on push): got err: %v", err)
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
			name:        "should reject empty Bitbucket webhook token",
			gitProvider: "bitbucket",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"password": []byte(""),
				},
			},
			expectError: true,
		},
		{
			name:        "test should reject empty Bitbucket username",
			gitProvider: "bitbucket",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"username": []byte(""),
					"password": []byte("token"),
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
			got := slices.Intersection(tt.in1, tt.in2)
			if got != tt.intersection {
				t.Errorf("Got slice intersection %d but expected length is %d", got, tt.intersection)
			}
		})
	}
}

func TestGeneratePACRepository(t *testing.T) {
	getComponent := func(repoUrl string, annotations map[string]string) appstudiov1alpha1.Component {
		return appstudiov1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "testcomponent",
				Namespace:   "workspace-name",
				Annotations: annotations,
			},
			Spec: appstudiov1alpha1.ComponentSpec{
				Source: appstudiov1alpha1.ComponentSource{
					ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
						GitSource: &appstudiov1alpha1.GitSource{
							URL: repoUrl,
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name                      string
		repoUrl                   string
		componentAnnotations      map[string]string
		pacConfig                 map[string][]byte
		expectedGitProviderConfig *pacv1alpha1.GitProvider
	}{
		{
			name:    "should create PaC repository for Github application",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig: nil,
		},
		{
			name:    "should create PaC repository for Github application even if Github webhook configured",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("ghp_token"),
			},
			expectedGitProviderConfig: nil,
		},
		{
			name:    "should create PaC repository for Github webhook",
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.com/user/test-component-repository", nil)),
				},
			},
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository/", nil)),
				},
				URL: "https://gitlab.com",
			},
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository", nil)),
				},
				URL: "https://gitlab.com",
			},
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil)),
				},
				URL: "https://gitlab.self-hosted.com",
			},
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil)),
				},
				URL: "https://gitlab.self-hosted-proxy.com",
			},
		},
		{
			name:    "should create PaC repository for Github application on self-hosted Github",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
				GitProviderAnnotationURL:  "https://github.self-hosted.com",
			},
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				URL: "https://github.self-hosted.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := getComponent(tt.repoUrl, tt.componentAnnotations)
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
			pacRepo, err := generatePACRepository(component, secret)

			if err != nil {
				t.Errorf("Failed to generate PaC repository object. Cause: %v", err)
			}

			if pacRepo.Name != component.Name {
				t.Errorf("Generated PaC repository must have the same name as corresponding component")
			}
			if pacRepo.Namespace != component.Namespace {
				t.Errorf("Generated PaC repository must have the same namespace as corresponding component")
			}

			expectedRepo := strings.TrimSuffix(strings.TrimSuffix(tt.repoUrl, ".git"), "/")
			if pacRepo.Spec.URL != expectedRepo {
				t.Errorf("Wrong git repository URL in PaC repository: %s, want %s", pacRepo.Spec.URL, expectedRepo)
			}
			if !reflect.DeepEqual(pacRepo.Spec.GitProvider, tt.expectedGitProviderConfig) {
				t.Errorf("Wrong git provider config in PaC repository: %#v, want %#v", pacRepo.Spec.GitProvider, tt.expectedGitProviderConfig)
			}
		})
	}
}

/*
add new
override existing one
override existing one and unset
*/

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

	component := getComponentData(componentConfig{})

	convertCustomParamsToMap := func(repository *pacv1alpha1.Repository) map[string]pacv1alpha1.Params {
		result := map[string]pacv1alpha1.Params{}
		for _, param := range *repository.Spec.Params {
			result[param.Name] = param
		}
		return result
	}

	t.Run("add to Spec.Params", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret)
		pacRepoAddParamWorkspaceName(log, repository, workspaceName)

		params := convertCustomParamsToMap(repository)
		param, ok := params[pacCustomParamAppstudioWorkspace]
		assert.Assert(t, ok)
		assert.Equal(t, workspaceName, param.Value)
	})

	t.Run("override existing workspace parameter, unset other fields btw", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret)
		params := []pacv1alpha1.Params{
			{
				Name:      pacCustomParamAppstudioWorkspace,
				Value:     "another_workspace",
				Filter:    "pac.event_type == \"pull_request\"",
				SecretRef: &pacv1alpha1.Secret{},
			},
		}
		repository.Spec.Params = &params

		pacRepoAddParamWorkspaceName(log, repository, workspaceName)

		existingParams := convertCustomParamsToMap(repository)
		param, ok := existingParams[pacCustomParamAppstudioWorkspace]
		assert.Assert(t, ok)
		assert.Equal(t, workspaceName, param.Value)

		assert.Equal(t, "", param.Filter)
		assert.Assert(t, param.SecretRef == nil)
	})
}

func TestGetGitProvider(t *testing.T) {
	getComponent := func(repoUrl, annotationValue string) appstudiov1alpha1.Component {
		componentMeta := metav1.ObjectMeta{
			Name:      "testcomponent",
			Namespace: "workspace-name",
		}
		if annotationValue != "" {
			componentMeta.Annotations = map[string]string{
				GitProviderAnnotationName: annotationValue,
			}
		}

		component := appstudiov1alpha1.Component{
			ObjectMeta: componentMeta,
			Spec: appstudiov1alpha1.ComponentSpec{
				Source: appstudiov1alpha1.ComponentSource{
					ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
						GitSource: &appstudiov1alpha1.GitSource{
							URL: repoUrl,
						},
					},
				},
			},
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
			name:             "should detect github provider via http url",
			componentRepoUrl: "https://github.com/user/test-component-repository",
			want:             "github",
		},
		{
			name:             "should detect github provider via git url",
			componentRepoUrl: "git@github.com:user/test-component-repository",
			want:             "github",
		},
		{
			name:             "should detect gitlab provider via http url",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider via git url",
			componentRepoUrl: "git@gitlab.com:user/test-component-repository",
			want:             "gitlab",
		},
		{
			name:             "should detect bitbucket provider via http url",
			componentRepoUrl: "https://bitbucket.org/user/test-component-repository",
			want:             "bitbucket",
		},
		{
			name:             "should detect bitbucket provider via git url",
			componentRepoUrl: "git@bitbucket.org:user/test-component-repository",
			want:             "bitbucket",
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
			name:                           "should detect bitbucket provider via annotation",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "bitbucket",
			want:                           "bitbucket",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := getComponent(tt.componentRepoUrl, tt.componentGitProviderAnnotation)
			got, err := getGitProvider(component)
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
		component.Spec.Source.GitSource = nil
		_, err := getGitProvider(component)
		if err == nil {
			t.Error("Expected error for nil source URL")
		}
	})
}
