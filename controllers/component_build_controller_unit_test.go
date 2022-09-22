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
	"testing"

	"github.com/redhat-appstudio/application-service/gitops"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
)

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
			name: "github",
			args: args{
				ctx:    context.Background(),
				gitURL: "git@github.com:redhat-appstudio/application-service.git",
			},
			wantErr:    true, //unsupported
			wantString: "",
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

func TestGeneratePipelineRun(t *testing.T) {
	tests := []struct {
		name          string
		onPull        bool
		devfileString string
		gitSource     *appstudiov1alpha1.GitSource
		want          string
	}{
		{
			name:   "pull-request-test",
			onPull: true,
			devfileString: `
schemaVersion: 2.2.0
metadata:
  name: nodejs
components:
  - name: outerloop-build
    image:
      imageName: nodejs-image:latest
      dockerfile:
        uri: "test/Dockerfile"
        buildContext: "."
`,
			want: `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  annotations:
    build.appstudio.redhat.com/commit_sha: '{{revision}}'
    build.appstudio.redhat.com/pull_request_number: '{{pull_request_number}}'
    build.appstudio.redhat.com/target_branch: '{{target_branch}}'
    pipelinesascode.tekton.dev/max-keep-runs: "3"
    pipelinesascode.tekton.dev/on-event: '[pull_request]'
    pipelinesascode.tekton.dev/on-target-branch: '[main,master]'
  creationTimestamp: null
  labels:
    appstudio.openshift.io/application: app
    appstudio.openshift.io/component: pull-request-test
    pipelines.appstudio.openshift.io/type: build
  name: pull-request-test-on-pull-request
  namespace: namespace
spec:
  params:
  - name: git-url
    value: '{{repo_url}}'
  - name: revision
    value: '{{revision}}'
  - name: output-image
    value: image:on-pr-{{revision}}
  - name: dockerfile
    value: test/Dockerfile
  - name: path-context
    value: .
  pipelineRef:
    bundle: bundle
    name: docker-build
  workspaces:
  - name: workspace
    persistentVolumeClaim:
      claimName: appstudio
    subPath: pull-request-test-on-pull-request-{{revision}}
  - name: registry-auth
    secret:
      secretName: redhat-appstudio-registry-pull-secret
status: {}
`,
		},
		{
			name:   "push-test",
			onPull: false,
			want: `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  annotations:
    build.appstudio.redhat.com/commit_sha: '{{revision}}'
    build.appstudio.redhat.com/target_branch: '{{target_branch}}'
    pipelinesascode.tekton.dev/max-keep-runs: "3"
    pipelinesascode.tekton.dev/on-event: '[push]'
    pipelinesascode.tekton.dev/on-target-branch: '[main,master]'
  creationTimestamp: null
  labels:
    appstudio.openshift.io/application: app
    appstudio.openshift.io/component: push-test
    pipelines.appstudio.openshift.io/type: build
  name: push-test-on-push
  namespace: namespace
spec:
  params:
  - name: git-url
    value: '{{repo_url}}'
  - name: revision
    value: '{{revision}}'
  - name: output-image
    value: image:{{revision}}
  pipelineRef:
    bundle: bundle
    name: docker-build
  workspaces:
  - name: workspace
    persistentVolumeClaim:
      claimName: appstudio
    subPath: push-test-on-push-{{revision}}
  - name: registry-auth
    secret:
      secretName: redhat-appstudio-registry-pull-secret
status: {}
`,
		},
		{
			name:      "custom-target-branch",
			gitSource: &appstudiov1alpha1.GitSource{Revision: "test-revision"},
			want: `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  annotations:
    build.appstudio.redhat.com/commit_sha: '{{revision}}'
    build.appstudio.redhat.com/target_branch: '{{target_branch}}'
    pipelinesascode.tekton.dev/max-keep-runs: "3"
    pipelinesascode.tekton.dev/on-event: '[push]'
    pipelinesascode.tekton.dev/on-target-branch: '[test-revision]'
  creationTimestamp: null
  labels:
    appstudio.openshift.io/application: app
    appstudio.openshift.io/component: custom-target-branch
    pipelines.appstudio.openshift.io/type: build
  name: custom-target-branch-on-push
  namespace: namespace
spec:
  params:
  - name: git-url
    value: '{{repo_url}}'
  - name: revision
    value: '{{revision}}'
  - name: output-image
    value: image:{{revision}}
  pipelineRef:
    bundle: bundle
    name: docker-build
  workspaces:
  - name: workspace
    persistentVolumeClaim:
      claimName: appstudio
    subPath: custom-target-branch-on-push-{{revision}}
  - name: registry-auth
    secret:
      secretName: redhat-appstudio-registry-pull-secret
status: {}
`,
		},
		{
			name:   "invalid-devfile-by-missing-schemaversion",
			onPull: true,
			devfileString: `
metadata:
  name: nodejs
components:
  - name: outerloop-build
    image:
      imageName: nodejs-image:latest
      dockerfile:
        uri: "test/Dockerfile"
        buildContext: "."
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := appstudiov1alpha1.Component{
				TypeMeta: v1.TypeMeta{
					Kind:       "Component",
					APIVersion: "appstudio.redhat.com/v1alpha1",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      tt.name,
					Namespace: "namespace",
				},
				Spec: appstudiov1alpha1.ComponentSpec{
					Application:    "app",
					ContainerImage: "image",
					Source: appstudiov1alpha1.ComponentSource{
						ComponentSourceUnion: appstudiov1alpha1.ComponentSourceUnion{
							GitSource: tt.gitSource,
						},
					},
				},
				Status: appstudiov1alpha1.ComponentStatus{Devfile: tt.devfileString},
			}
			got, err := GeneratePipelineRun(component, "bundle", tt.onPull)
			if tt.name == "invalid-devfile-by-missing-schemaversion" && err == nil {
				t.Errorf("devfile is invalid, but GeneratePipelineRun does not return an error.")
			} else if string(got) != tt.want {
				output := string(got)
				t.Errorf("TestGeneratePipelineRun() = %v, want %v", output, tt.want)
			}
		})
	}
}

func TestGenerateCommonStorage(t *testing.T) {
	t.Run("Must generate PVC for builds", func(t *testing.T) {
		name := "appstudio"
		namespace := "user-namespace"

		pvc := generateCommonStorage(name, namespace)

		if pvc.GetName() != name {
			t.Errorf("pvc should have given name")
		}
		if pvc.GetNamespace() != namespace {
			t.Errorf("pvc should be created in given namespace")
		}

		if len(pvc.Spec.AccessModes) != 1 {
			t.Errorf("pvc should have access mode specified")
		}
		if pvc.Spec.AccessModes[0] != "ReadWriteOnce" {
			t.Errorf("pvc access mode ReadWriteOnce expected")
		}

		if !pvc.Spec.Resources.Requests.Storage().Equal(resource.MustParse("1Gi")) {
			t.Errorf("pvc storage size 1Gi expected")
		}

		if *pvc.Spec.VolumeMode != corev1.PersistentVolumeFilesystem {
			t.Errorf("pvc volume mode PersistentVolumeFilesystem expected")
		}
	})
}

func TestValidatePaCConfiguration(t *testing.T) {
	ghAppPrivateKeyStub := "-----BEGIN RSA PRIVATE KEY-----_key-content_-----END RSA PRIVATE KEY-----"

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
