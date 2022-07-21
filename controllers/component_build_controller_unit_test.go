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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
			if got, err := getGitProvider(tt.args.gitURL); (got != tt.wantString) ||
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
		name   string
		onPull bool
		want   string
	}{
		{
			name:   "pull-request-test",
			onPull: true,
			want: `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  annotations:
    build.appstudio.redhat.com/commit_sha: '{{revision}}'
    build.appstudio.redhat.com/pull_request_number: '{{pull_request_number}}'
    build.appstudio.redhat.com/target_branch: '{{target_branch}}'
    pipelinesascode.tekton.dev/max-keep-runs: "3"
    pipelinesascode.tekton.dev/on-event: '[pull_request]'
    pipelinesascode.tekton.dev/on-target-branch: '[main]'
  creationTimestamp: null
  labels:
    build.appstudio.openshift.io/component: pull-request-test
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
  - name: path-context
    value: .
  - name: dockerfile
    value: Dockerfile
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
    pipelinesascode.tekton.dev/on-target-branch: '[main]'
  creationTimestamp: null
  labels:
    build.appstudio.openshift.io/component: push-test
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
  - name: path-context
    value: .
  - name: dockerfile
    value: Dockerfile
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GeneratePipelineRun(tt.name, "namespace", "bundle", "image", tt.onPull)
			if err != nil {
				t.Errorf("err")
			}
			if string(got) != tt.want {
				output := string(got)
				t.Errorf("TestGeneratePipelineRun() = %v, want %v", output, tt.want)
			}
		})
	}
}

func TestGenerateCommonStorage(t *testing.T) {
	t.Run("Must generate PVC for builds ", func(t *testing.T) {
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
