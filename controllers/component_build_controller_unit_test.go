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
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/redhat-appstudio/application-service/gitops"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
)

func TestGenerateInitialPipelineRunForComponent(t *testing.T) {
	component := &appstudiov1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-component",
			Namespace: "my-namespace",
			Annotations: map[string]string{
				"skip-initials-checks": "true",
			},
		},
		Spec: appstudiov1alpha1.ComponentSpec{
			Application:    "my-application",
			ContainerImage: "registry.io/username/image:tag",
			Source: appstudiov1alpha1.ComponentSource{
				ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
					GitSource: &appstudiov1alpha1.GitSource{
						URL:      "https://githost.com/user/repo.git",
						Revision: "custom-branch",
					},
				},
			},
		},
		Status: appstudiov1alpha1.ComponentStatus{
			Devfile: getMinimalDevfile(),
		},
	}
	pipelineRef := &tektonapi.PipelineRef{
		Name:   "pipeline-name",
		Bundle: "pipeline-bundle",
	}
	additionalParams := []tektonapi.Param{
		{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "2378a064bf6b66a8ffc650ad88d404cca24ade29"}},
		{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}},
	}

	pipelineRun, err := generateInitialPipelineRunForComponent(component, pipelineRef, additionalParams)
	if err != nil {
		t.Error("generateInitialPipelineRunForComponent(): Failed to genertate pipeline run")
	}

	if pipelineRun.GenerateName == "" {
		t.Error("generateInitialPipelineRunForComponent(): pipeline generatename must not be empty")
	}
	if pipelineRun.Namespace != "my-namespace" {
		t.Error("generateInitialPipelineRunForComponent(): pipeline namespace doesn't match")
	}

	if pipelineRun.Labels[ApplicationNameLabelName] != "my-application" {
		t.Errorf("generateInitialPipelineRunForComponent(): wrong %s label value", ApplicationNameLabelName)
	}
	if pipelineRun.Labels[ComponentNameLabelName] != "my-component" {
		t.Errorf("generateInitialPipelineRunForComponent(): wrong %s label value", ComponentNameLabelName)
	}
	if pipelineRun.Labels["pipelines.appstudio.openshift.io/type"] != "build" {
		t.Error("generateInitialPipelineRunForComponent(): wrong pipelines.appstudio.openshift.io/type label value")
	}

	if pipelineRun.Annotations["build.appstudio.redhat.com/target_branch"] != "custom-branch" {
		t.Error("generateInitialPipelineRunForComponent(): wrong build.appstudio.redhat.com/target_branch annotation value")
	}
	if pipelineRun.Annotations["build.appstudio.redhat.com/pipeline_name"] != "pipeline-name" {
		t.Error("generateInitialPipelineRunForComponent(): wrong build.appstudio.redhat.com/pipeline_name annotation value")
	}
	if pipelineRun.Annotations["build.appstudio.redhat.com/bundle"] != "pipeline-bundle" {
		t.Error("generateInitialPipelineRunForComponent(): wrong build.appstudio.redhat.com/bundle annotation value")
	}

	if pipelineRun.Spec.PipelineRef.Name != "pipeline-name" {
		t.Error("generateInitialPipelineRunForComponent(): wrong pipeline name in pipeline reference")
	}
	if pipelineRun.Spec.PipelineRef.Bundle != "pipeline-bundle" {
		t.Error("generateInitialPipelineRunForComponent(): wrong pipeline bundle in pipeline reference")
	}

	if len(pipelineRun.Spec.Params) != 5 {
		t.Error("generateInitialPipelineRunForComponent(): wrong number of pipeline params")
	}
	for _, param := range pipelineRun.Spec.Params {
		switch param.Name {
		case "git-url":
			if param.Value.StringVal != "https://githost.com/user/repo.git" {
				t.Errorf("generateInitialPipelineRunForComponent(): wrong pipeline parameter %s", param.Name)
			}
		case "revision":
			if param.Value.StringVal != "2378a064bf6b66a8ffc650ad88d404cca24ade29" {
				t.Errorf("generateInitialPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "output-image":
			if !strings.HasPrefix(param.Value.StringVal, "registry.io/username/image:initial-build-") {
				t.Errorf("generateInitialPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "rebuild":
			if param.Value.StringVal != "true" {
				t.Errorf("generateInitialPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "skip-checks":
			if param.Value.StringVal != "true" {
				t.Errorf("generateInitialPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		default:
			t.Errorf("generateInitialPipelineRunForComponent(): unexpected pipeline parameter %v", param)
		}
	}

	if len(pipelineRun.Spec.Workspaces) != 2 {
		t.Error("generateInitialPipelineRunForComponent(): wrong number of pipeline workspaces")
	}
	for _, workspace := range pipelineRun.Spec.Workspaces {
		if workspace.Name == "workspace" {
			continue
		}
		if workspace.Name == "registry-auth" {
			continue
		}
		t.Errorf("generateInitialPipelineRunForComponent(): unexpected pipeline workspaces %v", workspace)
	}
}

func TestMergeAndSortTektonParams(t *testing.T) {
	tests := []struct {
		name       string
		existing   []tektonapi.Param
		additional []tektonapi.Param
		want       []tektonapi.Param
	}{
		{
			name: "should merge two different parameters lists",
			existing: []tektonapi.Param{
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "main"}},
			},
			additional: []tektonapi.Param{
				{Name: "dockerfile", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "docker/Dockerfile"}},
				{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}},
			},
			want: []tektonapi.Param{
				{Name: "dockerfile", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "docker/Dockerfile"}},
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
				{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "main"}},
			},
		},
		{
			name: "should append empty parameters list",
			existing: []tektonapi.Param{
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "main"}},
			},
			additional: []tektonapi.Param{},
			want: []tektonapi.Param{
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "main"}},
			},
		},
		{
			name: "should sort parameters list",
			existing: []tektonapi.Param{
				{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}},
				{Name: "dockerfile", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "docker/Dockerfile"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "main"}},
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
			},
			additional: []tektonapi.Param{},
			want: []tektonapi.Param{
				{Name: "dockerfile", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "docker/Dockerfile"}},
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
				{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "main"}},
			},
		},
		{
			name: "should override existing parameters",
			existing: []tektonapi.Param{
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "main"}},
				{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "false"}},
			},
			additional: []tektonapi.Param{
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "2378a064bf6b66a8ffc650ad88d404cca24ade29"}},
				{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}},
			},
			want: []tektonapi.Param{
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
				{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "2378a064bf6b66a8ffc650ad88d404cca24ade29"}},
			},
		},
		{
			name: "should append and override parameters",
			existing: []tektonapi.Param{
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "main"}},
			},
			additional: []tektonapi.Param{
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "2378a064bf6b66a8ffc650ad88d404cca24ade29"}},
				{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}},
			},
			want: []tektonapi.Param{
				{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "https://githost.com/user/repo.git"}},
				{Name: "rebuild", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}},
				{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "2378a064bf6b66a8ffc650ad88d404cca24ade29"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeAndSortTektonParams(tt.existing, tt.additional)
			if len(got) != len(tt.want) {
				t.Errorf("mergeAndSortTektonParams(): got %v, want %v", got, tt.want)
			}
			for i := range got {
				if !reflect.DeepEqual(got[i], tt.want[i]) {
					t.Errorf("mergeAndSortTektonParams(): got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestGetGitProvider(t *testing.T) {
	type args struct {
		ctx    context.Context
		gitURL string
	}
	tests := []struct {
		name       string
		args       args
		wantErr    bool
		wantString string
	}{
		{
			name: "github ssh",
			args: args{
				ctx:    context.Background(),
				gitURL: "git@github.com:redhat-appstudio/application-service.git",
			},
			wantErr:    false,
			wantString: "https://github.com",
		},
		{
			name: "gitlab ssh",
			args: args{
				ctx:    context.Background(),
				gitURL: "git@gitlab.com:namespace/project-name.git",
			},
			wantErr:    false,
			wantString: "https://gitlab.com",
		},
		{
			name: "bitbucket ssh",
			args: args{
				ctx:    context.Background(),
				gitURL: "git@bitbucket.org:organization/project-name.git",
			},
			wantErr:    false,
			wantString: "https://bitbucket.org",
		},
		{
			name: "github https",
			args: args{
				ctx:    context.Background(),
				gitURL: "https://github.com/redhat-appstudio/application-service.git",
			},
			wantErr:    false,
			wantString: "https://github.com",
		},
		{
			name: "bitbucket https",
			args: args{
				ctx:    context.Background(),
				gitURL: "https://sbose78@bitbucket.org/sbose78/appstudio.git",
			},
			wantErr:    false,
			wantString: "https://bitbucket.org",
		},
		{
			name: "no scheme",
			args: args{
				ctx:    context.Background(),
				gitURL: "github.com/redhat-appstudio/application-service.git",
			},
			wantErr:    true, //fully qualified URL is a must
			wantString: "",
		},
		{
			name: "invalid url",
			args: args{
				ctx:    context.Background(),
				gitURL: "not-even-a-url",
			},
			wantErr:    true, //fully qualified URL is a must
			wantString: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := getGitProviderUrl(tt.args.gitURL); (got != tt.wantString) ||
				(tt.wantErr == true && err == nil) ||
				(tt.wantErr == false && err != nil) {
				t.Errorf("UpdateServiceAccountIfSecretNotLinked() Got Error: = %v, want %v ; Got String:  = %v , want %v", err, tt.wantErr, got, tt.wantString)
			}
		})
	}
}

func TestUpdateServiceAccountIfSecretNotLinked(t *testing.T) {
	type args struct {
		gitSecretName  string
		serviceAccount *corev1.ServiceAccount
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "present",
			args: args{
				gitSecretName: "present",
				serviceAccount: &corev1.ServiceAccount{
					Secrets: []corev1.ObjectReference{
						{
							Name: "present",
						},
					},
				},
			},
			want: false, // since it was present, this implies the SA wasn't updated.
		},
		{
			name: "not present",
			args: args{
				gitSecretName: "not-present",
				serviceAccount: &corev1.ServiceAccount{
					Secrets: []corev1.ObjectReference{
						{
							Name: "something-else",
						},
					},
				},
			},
			want: true, // since it wasn't present, this implies the SA was updated.
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := updateServiceAccountIfSecretNotLinked(tt.args.gitSecretName, tt.args.serviceAccount); got != tt.want {
				t.Errorf("UpdateServiceAccountIfSecretNotLinked() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidatePaCConfiguration(t *testing.T) {
	const ghAppPrivateKeyStub = "-----BEGIN RSA PRIVATE KEY-----_key-content_-----END RSA PRIVATE KEY-----"

	tests := []struct {
		name        string
		gitProvider string
		config      map[string][]byte
		expectError bool
	}{
		{
			name:        "should accept GitHub application configuration",
			gitProvider: "github",
			config: map[string][]byte{
				gitops.PipelinesAsCode_githubAppIdKey:   []byte("12345"),
				gitops.PipelinesAsCode_githubPrivateKey: []byte(ghAppPrivateKeyStub),
			},
			expectError: false,
		},
		{
			name:        "should accept GitHub application configuration with end line",
			gitProvider: "github",
			config: map[string][]byte{
				gitops.PipelinesAsCode_githubAppIdKey:   []byte("12345"),
				gitops.PipelinesAsCode_githubPrivateKey: []byte(ghAppPrivateKeyStub + "\n"),
			},
			expectError: false,
		},
		{
			name:        "should accept GitHub webhook configuration",
			gitProvider: "github",
			config: map[string][]byte{
				"github.token": []byte("ghp_token"),
			},
			expectError: false,
		},
		{
			name:        "should reject empty GitHub webhook token",
			gitProvider: "github",
			config: map[string][]byte{
				"github.token": []byte(""),
			},
			expectError: true,
		},
		{
			name:        "should accept GitHub application configuration if both GitHub application and webhook configured",
			gitProvider: "github",
			config: map[string][]byte{
				gitops.PipelinesAsCode_githubAppIdKey:   []byte("12345"),
				gitops.PipelinesAsCode_githubPrivateKey: []byte(ghAppPrivateKeyStub),
				"github.token":                          []byte("ghp_token"),
				"gitlab.token":                          []byte("token"),
			},
			expectError: false,
		},
		{
			name:        "should reject GitHub application configuration if GitHub id is missing",
			gitProvider: "github",
			config: map[string][]byte{
				gitops.PipelinesAsCode_githubPrivateKey: []byte(ghAppPrivateKeyStub),
				"github.token":                          []byte("ghp_token"),
			},
			expectError: true,
		},
		{
			name:        "should reject GitHub application configuration if GitHub application private key is missing",
			gitProvider: "github",
			config: map[string][]byte{
				gitops.PipelinesAsCode_githubAppIdKey: []byte("12345"),
				"github.token":                        []byte("ghp_token"),
			},
			expectError: true,
		},
		{
			name:        "should reject GitHub application configuration if GitHub application id is invalid",
			gitProvider: "github",
			config: map[string][]byte{
				gitops.PipelinesAsCode_githubAppIdKey:   []byte("12ab"),
				gitops.PipelinesAsCode_githubPrivateKey: []byte(ghAppPrivateKeyStub),
			},
			expectError: true,
		},
		{
			name:        "should reject GitHub application configuration if GitHub application application private key is invalid",
			gitProvider: "github",
			config: map[string][]byte{
				gitops.PipelinesAsCode_githubAppIdKey:   []byte("12345"),
				gitops.PipelinesAsCode_githubPrivateKey: []byte("private-key"),
			},
			expectError: true,
		},
		{
			name:        "should accept GitLab webhook configuration",
			gitProvider: "gitlab",
			config: map[string][]byte{
				"gitlab.token": []byte("token"),
			},
			expectError: false,
		},
		{
			name:        "should accept GitLab webhook configuration even if other providers configured",
			gitProvider: "gitlab",
			config: map[string][]byte{
				gitops.PipelinesAsCode_githubAppIdKey:   []byte("12345"),
				gitops.PipelinesAsCode_githubPrivateKey: []byte(ghAppPrivateKeyStub),
				"github.token":                          []byte("ghp_token"),
				"gitlab.token":                          []byte("token"),
				"bitbucket.token":                       []byte("token2"),
			},
			expectError: false,
		},
		{
			name:        "should reject empty GitLab webhook token",
			gitProvider: "gitlab",
			config: map[string][]byte{
				"gitlab.token": []byte(""),
			},
			expectError: true,
		},
		{
			name:        "should accept Bitbucket webhook configuration even if other providers configured",
			gitProvider: "bitbucket",
			config: map[string][]byte{
				gitops.PipelinesAsCode_githubAppIdKey:   []byte("12345"),
				gitops.PipelinesAsCode_githubPrivateKey: []byte(ghAppPrivateKeyStub),
				"github.token":                          []byte("ghp_token"),
				"gitlab.token":                          []byte("token"),
				"bitbucket.token":                       []byte("token2"),
				"username":                              []byte("user"),
			},
			expectError: false,
		},
		{
			name:        "should reject empty Bitbucket webhook token",
			gitProvider: "gitlab",
			config: map[string][]byte{
				"bitbucket.token": []byte(""),
				"username":        []byte("user"),
			},
			expectError: true,
		},
		{
			name:        "should reject Bitbucket webhook configuration with empty username",
			gitProvider: "gitlab",
			config: map[string][]byte{
				"bitbucket.token": []byte("token"),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePaCConfiguration(tt.gitProvider, tt.config)
			if err != nil {
				if !tt.expectError {
					t.Errorf("Expected that the configuration %#v fro provider %s shoould be valid", tt.config, tt.gitProvider)
				}
			} else {
				if tt.expectError {
					t.Errorf("Expected that the configuration %#v fro provider %s fails", tt.config, tt.gitProvider)
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
