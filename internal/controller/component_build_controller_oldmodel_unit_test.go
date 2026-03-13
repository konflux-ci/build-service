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

// TODO remove whole file after only new model is used
package controllers

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/konflux-ci/build-service/pkg/common"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// TODO remove after only new model is used
func TestReadBuildStatusOldModel(t *testing.T) {
	tests := []struct {
		name                       string
		buildStatusAnnotationValue string
		want                       *BuildStatus
	}{
		{
			name:                       "should be able to read build status with all fields",
			buildStatusAnnotationValue: "{\"pac\":{\"state\":\"enabled\",\"configuration-time\":\"time\",\"error-id\":5,\"error-message\":\"pac-error\"},\"message\":\"done\"}",
			want: &BuildStatus{
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
			buildStatusAnnotationValue: "{\"pac\":{\"build-start-time\":\"time\"}",
			want:                       &BuildStatus{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := &compapiv1alpha1.Component{
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

// TODO remove after only new model is used
func TestWriteBuildStatusOldModel(t *testing.T) {
	tests := []struct {
		name        string
		component   *compapiv1alpha1.Component
		buildStatus *BuildStatus
		want        string
	}{
		{
			name: "should be able to write build status with all fields",
			component: &compapiv1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-component",
					Namespace:   "my-namespace",
					Annotations: map[string]string{},
				},
			},
			buildStatus: &BuildStatus{
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
			want: "{\"pac\":{\"state\":\"enabled\",\"configuration-time\":\"time\",\"error-id\":5,\"error-message\":\"pac-error\"},\"message\":\"done\"}",
		},
		{
			name: "should be able to write build status when annotations is nil",
			component: &compapiv1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-component",
					Namespace: "my-namespace",
				},
			},
			buildStatus: &BuildStatus{
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
			want: "{\"pac\":{\"state\":\"enabled\",\"configuration-time\":\"time\",\"error-id\":5,\"error-message\":\"pac-error\"},\"message\":\"done\"}",
		},
		{
			name: "should be able to overwrite build status",
			component: &compapiv1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-component",
					Namespace: "my-namespace",
					Annotations: map[string]string{
						BuildStatusAnnotationName: "{\"pac\":{\"state\":\"error\"},\"message\":\"done\"}",
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

// TODO remove after only new model is used
func TestGeneratePaCPipelineRunForComponentOldModel(t *testing.T) {
	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-component",
			Namespace: "my-namespace",
			Annotations: map[string]string{
				"skip-initial-checks":          "true",
				GitProviderAnnotationName:      "github",
				defaultBuildPipelineAnnotation: testPipelineAnnotation,
			},
		},
		Spec: compapiv1alpha1.ComponentSpec{
			Application:    "my-application",
			ContainerImage: "registry.io/username/image:tag",
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitSource: &compapiv1alpha1.GitSource{
						URL:           "https://githost.com/user/repo.git",
						Context:       "./base_context",
						DockerfileURL: "containerFile",
					},
				},
			},
		},
		Status: compapiv1alpha1.ComponentStatus{},
	}
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
	branchName := "custom-branch"
	ResetTestGitProviderClient()

	pipelineRun, err := generatePaCPipelineRunForComponentOldModel(component, pipelineSpec, []string{"add-param1", "add-param2", "non-existing"}, branchName, testGitProviderClient, true)
	if err != nil {
		t.Error("generatePaCPipelineRunForComponent(): Failed to generate pipeline run")
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
}

// TODO remove after only new model is used
func TestGeneratePaCPipelineRunForComponent_ShouldStopIfTargetBranchIsNotSetOldModel(t *testing.T) {
	_, err := generatePaCPipelineRunForComponentOldModel(nil, nil, nil, "", nil, true)
	if err == nil {
		t.Errorf("generatePaCPipelineRunForComponent(): expected error")
	}
}

// TODO remove after only new model is used
func TestGenerateCelExpressionForPipelineOldModel(t *testing.T) {
	componentKey := types.NamespacedName{Namespace: "test-ns", Name: "component-name"}
	ResetTestGitProviderClient()

	tests := []struct {
		name              string
		component         *compapiv1alpha1.Component
		targetBranch      string
		isDockerfileExist func(repoUrl, branch, dockerfilePath string) (bool, error)
		wantOnPullError   bool
		wantOnPushError   bool
		wantOnPull        string
		wantOnPush        string
	}{
		{
			name: "should generate cel expression for component that occupies whole git repository",
			component: func() *compapiv1alpha1.Component {
				component := getSampleComponentDataOldModel(componentKey)
				return component
			}(),
			targetBranch: "my-branch",
			wantOnPull:   `event == "pull_request" && target_branch == "my-branch"`,
			wantOnPush:   `event == "push" && target_branch == "my-branch"`,
		},
		{
			name: "should generate cel expression for component with context directory, without dokerfile",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				return component
			}(),
			targetBranch: "my-branch",
			wantOnPull:   `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() )`,
			wantOnPush:   `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-push.yaml".pathChanged() )`,
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile in context directory",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "dockerfile/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return true, nil
			},
			wantOnPull: `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() )`,
			wantOnPush: `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-push.yaml".pathChanged() )`,
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile outside context directory",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "docker-root-dir/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, nil
			},
			wantOnPull: `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() || "docker-root-dir/Dockerfile".pathChanged() )`,
			wantOnPush: `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-push.yaml".pathChanged() || "docker-root-dir/Dockerfile".pathChanged() )`,
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile outside git repository",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "https://host.com:1234/files/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			wantOnPull:   `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() )`,
			wantOnPush:   `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-push.yaml".pathChanged() )`,
		},
		{
			name: "should fail to generate cel expression for component if isFileExist fails",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "non-existing/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, fmt.Errorf("Failed to check file existance")
			},
			wantOnPullError: true,
			wantOnPushError: true,
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

			got, err := generateCelExpressionForPipelineOldModel(tt.component, testGitProviderClient, tt.targetBranch, true)
			if err != nil {
				if !tt.wantOnPullError {
					t.Errorf("generateCelExpressionForPipeline(on pull): got err: %v", err)
				}
			} else {
				if got != tt.wantOnPull {
					t.Errorf("generateCelExpressionForPipeline(on pull): got '%s', want '%s'", got, tt.wantOnPull)
				}
			}

			got, err = generateCelExpressionForPipelineOldModel(tt.component, testGitProviderClient, tt.targetBranch, false)
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

// TODO remove after only new model is used
func TestGeneratePACRepositoryOldModel(t *testing.T) {
	getComponent := func(repoUrl string, annotations map[string]string) compapiv1alpha1.Component {
		return compapiv1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "testcomponent",
				Namespace:   "workspace-name",
				Annotations: annotations,
			},
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitSource: &compapiv1alpha1.GitSource{
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
			name:    "should create PaC repository for GitHub application",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig: nil,
		},
		{
			name:    "should create PaC repository for GitHub application even if Github webhook configured",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("ghp_token"),
			},
			expectedGitProviderConfig: nil,
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.com/user/test-component-repository", nil), false),
				},
				URL:  "",
				Type: "github",
			},
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
			expectedGitProviderConfig: nil,
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://github.self-hosted.com",
				Type: "github",
			},
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://github.self-hosted-proxy.com",
				Type: "github",
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository/", nil), false),
				},
				URL:  "",
				Type: "gitlab",
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository", nil), false),
				},
				Type: "gitlab",
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://gitlab.self-hosted.com",
				Type: "gitlab",
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://gitlab.self-hosted-proxy.com",
				Type: "gitlab",
			},
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://gitlab.self-hosted-proxy.com",
				Type: "gitlab",
			},
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
					Key:  getWebhookSecretKeyForComponent(getComponent("https://forgejo.example.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://forgejo.example.com",
				Type: "gitea",
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
			pacRepo, err := generatePACRepository(component, secret, nil, false)

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
		})
	}
}

// TODO remove after only new model is used
func TestPaCRepoAddParamWorkspaceOldModel(t *testing.T) {
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

	component := getComponentDataOldModel(componentConfigOldModel{})

	convertCustomParamsToMap := func(repository *pacv1alpha1.Repository) map[string]pacv1alpha1.Params {
		result := map[string]pacv1alpha1.Params{}
		for _, param := range *repository.Spec.Params {
			result[param.Name] = param
		}
		return result
	}

	t.Run("add to Spec.Params", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret, nil, false)
		pacRepoAddParamWorkspaceName(repository, workspaceName)

		params := convertCustomParamsToMap(repository)
		param, ok := params[pacCustomParamAppstudioWorkspace]
		assert.Assert(t, ok)
		assert.Equal(t, workspaceName, param.Value)
	})

	t.Run("override existing workspace parameter, unset other fields btw", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret, nil, false)
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

// TODO remove after only new model is used
func TestGetGitProviderOldModel(t *testing.T) {
	getComponent := func(repoUrl, annotationValue string) compapiv1alpha1.Component {
		componentMeta := metav1.ObjectMeta{
			Name:      "testcomponent",
			Namespace: "workspace-name",
		}
		if annotationValue != "" {
			componentMeta.Annotations = map[string]string{
				GitProviderAnnotationName: annotationValue,
			}
		}

		component := compapiv1alpha1.Component{
			ObjectMeta: componentMeta,
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitSource: &compapiv1alpha1.GitSource{
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
			expectError:      true,
			want:             "github",
		},
		{
			name:             "should detect non-standard github provider via http url",
			componentRepoUrl: "https://cooler.github.my-company.com/user/test-component-repository",
			want:             "github",
		},
		{
			name:             "should detect non-standard github provider via git url",
			componentRepoUrl: "git@cooler.github.my-company.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:             "should detect gitlab provider via http url",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider via git url",
			componentRepoUrl: "git@gitlab.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:             "should detect non-standard gitlab provider via http url",
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
			got, err := getGitProvider(component, false)
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
		_, err := getGitProvider(component, false)
		if err == nil {
			t.Error("Expected error for nil source URL")
		}
	})
}
