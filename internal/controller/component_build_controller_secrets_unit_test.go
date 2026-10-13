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
	"context"
	"fmt"
	"testing"

	compv1alpha1 "github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	"github.com/konflux-ci/build-service/pkg/common"
)

// newScheme creates a runtime.Scheme with the types needed by the secrets tests.
func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = compv1alpha1.AddToScheme(s)
	_ = compapiv1alpha1.AddToScheme(s)
	_ = pacv1alpha1.AddToScheme(s)
	return s
}

// newFakeEventRecorder returns a no-op EventRecorder suitable for unit tests.
func newFakeEventRecorder() events.EventRecorder {
	return &events.FakeRecorder{}
}

// --- Tests for getPaCIncomingSecretName ---

func TestGetPaCIncomingSecretName(t *testing.T) {
	tests := []struct {
		repoName string
		want     string
	}{
		{"my-repo", "my-repo-incoming"},
		{"", "-incoming"},
		{"abc", "abc-incoming"},
	}
	for _, tt := range tests {
		got := getPaCIncomingSecretName(tt.repoName)
		if got != tt.want {
			t.Errorf("getPaCIncomingSecretName(%q) = %q, want %q", tt.repoName, got, tt.want)
		}
	}
}

// --- Tests for ensureIncomingSecret (new model) ---

func TestEnsureIncomingSecret_FindRepositoryError(t *testing.T) {
	s := newScheme()

	// Interceptor that makes List fail (findPaCRepositoryForComponent uses List).
	funcs := interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return fmt.Errorf("simulated list failure")
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	_, _, err := reconciler.ensureIncomingSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when findPaCRepositoryForComponent fails, got nil")
	}
}

func TestEnsureIncomingSecret_GetSecretNonNotFoundError(t *testing.T) {
	s := newScheme()

	// Pre-create a PaC Repository so findPaCRepositoryForComponent succeeds.
	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "pac-repo", Namespace: "ns1"},
		Spec:       pacv1alpha1.RepositorySpec{URL: "https://github.com/org/repo"},
	}

	// Interceptor that makes Get fail for secrets with a non-NotFound error.
	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated get failure")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(repo).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	_, _, err := reconciler.ensureIncomingSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Get returns non-NotFound error, got nil")
	}
}

func TestEnsureIncomingSecret_CreateSecretError(t *testing.T) {
	s := newScheme()

	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-repo",
			Namespace: "ns1",
			// Repository needs UID for SetOwnerReference to succeed.
			UID: types.UID("test-uid-123"),
		},
		Spec: pacv1alpha1.RepositorySpec{URL: "https://github.com/org/repo"},
	}

	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated create failure")
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(repo).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	_, _, err := reconciler.ensureIncomingSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Create fails, got nil")
	}
}

func TestEnsureIncomingSecret_Success(t *testing.T) {
	s := newScheme()

	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-repo",
			Namespace: "ns1",
			UID:       types.UID("test-uid-123"),
		},
		Spec: pacv1alpha1.RepositorySpec{URL: "https://github.com/org/repo"},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(repo).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	secret, needsReconcile, err := reconciler.ensureIncomingSecret(context.Background(), component)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !needsReconcile {
		t.Error("expected needsReconcile=true when a new secret is created")
	}
	if secret == nil {
		t.Fatal("expected non-nil secret")
	}
}

func TestEnsureIncomingSecret_AlreadyExists(t *testing.T) {
	s := newScheme()

	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-repo",
			Namespace: "ns1",
			UID:       types.UID("test-uid-123"),
		},
		Spec: pacv1alpha1.RepositorySpec{URL: "https://github.com/org/repo"},
	}

	incomingSecretName := getPaCIncomingSecretName(repo.Name)
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: incomingSecretName, Namespace: "ns1"},
		Data:       map[string][]byte{pacIncomingSecretKey: []byte("existing-value")},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(repo, existingSecret).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	secret, needsReconcile, err := reconciler.ensureIncomingSecret(context.Background(), component)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if needsReconcile {
		t.Error("expected needsReconcile=false when secret already exists")
	}
	if secret == nil {
		t.Fatal("expected non-nil secret")
	}
}

// --- Tests for ensureIncomingSecret (old model) ---

func TestEnsureIncomingSecretOldModel_FindRepositoryError(t *testing.T) {
	s := newScheme()

	funcs := interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return fmt.Errorf("simulated list failure")
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconcilerOldModel{Client: fakeClient, Scheme: s}

	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compapiv1alpha1.ComponentSpec{
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitSource: &compapiv1alpha1.GitSource{URL: "https://github.com/org/repo"},
				},
			},
		},
	}

	_, _, err := reconciler.ensureIncomingSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when findPaCRepositoryForComponent fails, got nil")
	}
}

func TestEnsureIncomingSecretOldModel_GetSecretNonNotFoundError(t *testing.T) {
	s := newScheme()

	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "pac-repo", Namespace: "ns1"},
		Spec:       pacv1alpha1.RepositorySpec{URL: "https://github.com/org/repo"},
	}

	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated get failure")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(repo).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconcilerOldModel{Client: fakeClient, Scheme: s}

	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compapiv1alpha1.ComponentSpec{
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitSource: &compapiv1alpha1.GitSource{URL: "https://github.com/org/repo"},
				},
			},
		},
	}

	_, _, err := reconciler.ensureIncomingSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Get returns non-NotFound error, got nil")
	}
}

func TestEnsureIncomingSecretOldModel_CreateSecretError(t *testing.T) {
	s := newScheme()

	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-repo",
			Namespace: "ns1",
			UID:       types.UID("test-uid-123"),
		},
		Spec: pacv1alpha1.RepositorySpec{URL: "https://github.com/org/repo"},
	}

	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated create failure")
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(repo).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconcilerOldModel{Client: fakeClient, Scheme: s}

	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compapiv1alpha1.ComponentSpec{
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitSource: &compapiv1alpha1.GitSource{URL: "https://github.com/org/repo"},
				},
			},
		},
	}

	_, _, err := reconciler.ensureIncomingSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Create fails, got nil")
	}
}

// --- Tests for ensureWebhookSecret (new model) ---

func TestEnsureWebhookSecret_GetNonNotFoundError(t *testing.T) {
	s := newScheme()

	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated get failure")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	_, err := reconciler.ensureWebhookSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Get returns non-NotFound error, got nil")
	}
}

func TestEnsureWebhookSecret_CreateErrorOnNotFound(t *testing.T) {
	s := newScheme()

	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated create failure")
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	_, err := reconciler.ensureWebhookSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Create fails, got nil")
	}
}

func TestEnsureWebhookSecret_UpdateError(t *testing.T) {
	s := newScheme()

	// Pre-create the webhook secrets secret without the component's key.
	webhookSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pipelinesAsCodeWebhooksSecretName,
			Namespace: "ns1",
		},
		Data: map[string][]byte{},
	}

	funcs := interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated update failure")
			}
			return c.Update(ctx, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(webhookSecret).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	_, err := reconciler.ensureWebhookSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Update fails, got nil")
	}
}

func TestEnsureWebhookSecret_ExistingKeyReturned(t *testing.T) {
	s := newScheme()

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}
	secretKey := getWebhookSecretKeyForComponent(*component)

	webhookSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pipelinesAsCodeWebhooksSecretName,
			Namespace: "ns1",
		},
		Data: map[string][]byte{
			secretKey: []byte("existing-webhook-secret"),
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(webhookSecret).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	result, err := reconciler.ensureWebhookSecret(context.Background(), component)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "existing-webhook-secret" {
		t.Errorf("expected 'existing-webhook-secret', got %q", result)
	}
}

func TestEnsureWebhookSecret_SuccessNewKey(t *testing.T) {
	s := newScheme()

	webhookSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pipelinesAsCodeWebhooksSecretName,
			Namespace: "ns1",
		},
		Data: map[string][]byte{},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(webhookSecret).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	result, err := reconciler.ensureWebhookSecret(context.Background(), component)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty webhook secret string")
	}
	// The generated secret should be a 40-character hex string (20 bytes * 2).
	if len(result) != 40 {
		t.Errorf("expected 40-character hex string, got length %d", len(result))
	}
}

// --- Tests for ensureWebhookSecret (old model) ---

func TestEnsureWebhookSecretOldModel_GetNonNotFoundError(t *testing.T) {
	s := newScheme()

	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated get failure")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconcilerOldModel{Client: fakeClient, Scheme: s}

	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compapiv1alpha1.ComponentSpec{
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitSource: &compapiv1alpha1.GitSource{URL: "https://github.com/org/repo"},
				},
			},
		},
	}

	_, err := reconciler.ensureWebhookSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Get returns non-NotFound error, got nil")
	}
}

func TestEnsureWebhookSecretOldModel_CreateErrorOnNotFound(t *testing.T) {
	s := newScheme()

	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated create failure")
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconcilerOldModel{Client: fakeClient, Scheme: s}

	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compapiv1alpha1.ComponentSpec{
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitSource: &compapiv1alpha1.GitSource{URL: "https://github.com/org/repo"},
				},
			},
		},
	}

	_, err := reconciler.ensureWebhookSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Create fails, got nil")
	}
}

func TestEnsureWebhookSecretOldModel_UpdateError(t *testing.T) {
	s := newScheme()

	webhookSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pipelinesAsCodeWebhooksSecretName,
			Namespace: "ns1",
		},
		Data: map[string][]byte{},
	}

	funcs := interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated update failure")
			}
			return c.Update(ctx, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(webhookSecret).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconcilerOldModel{Client: fakeClient, Scheme: s}

	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compapiv1alpha1.ComponentSpec{
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitSource: &compapiv1alpha1.GitSource{URL: "https://github.com/org/repo"},
				},
			},
		},
	}

	_, err := reconciler.ensureWebhookSecret(context.Background(), component)
	if err == nil {
		t.Fatal("expected error when Client.Update fails, got nil")
	}
}

// --- Tests for lookupGHAppSecret (new model) ---

func TestLookupGHAppSecret_GetNonNotFoundError(t *testing.T) {
	s := newScheme()

	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated get failure")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconciler{
		Client:        fakeClient,
		Scheme:        s,
		EventRecorder: newFakeEventRecorder(),
	}

	_, err := reconciler.lookupGHAppSecret(context.Background())
	if err == nil {
		t.Fatal("expected error when Client.Get returns non-NotFound error, got nil")
	}
	// Should NOT be a boerror (it's an unexpected API error).
	if boerrors.IsBuildOpError(err, boerrors.EPaCSecretNotFound) {
		t.Error("unexpected boerror type for non-NotFound error")
	}
}

func TestLookupGHAppSecret_NotFoundReturnsBoError(t *testing.T) {
	s := newScheme()

	// No secret exists in the fake client, so Get returns NotFound.
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	reconciler := &ComponentBuildReconciler{
		Client:        fakeClient,
		Scheme:        s,
		EventRecorder: newFakeEventRecorder(),
	}

	_, err := reconciler.lookupGHAppSecret(context.Background())
	if err == nil {
		t.Fatal("expected error when secret is not found, got nil")
	}
	if !boerrors.IsBuildOpError(err, boerrors.EPaCSecretNotFound) {
		t.Errorf("expected EPaCSecretNotFound boerror, got: %v", err)
	}
}

func TestLookupGHAppSecret_Success(t *testing.T) {
	s := newScheme()

	pacSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.PipelinesAsCodeGitHubAppSecretName,
			Namespace: common.BuildServiceNamespaceName,
		},
		Data: map[string][]byte{
			"github-application-id": []byte("12345"),
			"github-private-key":    []byte("private-key-data"),
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(pacSecret).Build()
	reconciler := &ComponentBuildReconciler{
		Client:        fakeClient,
		Scheme:        s,
		EventRecorder: newFakeEventRecorder(),
	}

	result, err := reconciler.lookupGHAppSecret(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil secret")
	}
	if result.Name != common.PipelinesAsCodeGitHubAppSecretName {
		t.Errorf("unexpected secret name: %s", result.Name)
	}
}

// --- Tests for lookupGHAppSecret (old model) ---

func TestLookupGHAppSecretOldModel_GetNonNotFoundError(t *testing.T) {
	s := newScheme()

	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return fmt.Errorf("simulated get failure")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconcilerOldModel{
		Client:        fakeClient,
		Scheme:        s,
		EventRecorder: newFakeEventRecorder(),
	}

	_, err := reconciler.lookupGHAppSecret(context.Background())
	if err == nil {
		t.Fatal("expected error when Client.Get returns non-NotFound error, got nil")
	}
	if boerrors.IsBuildOpError(err, boerrors.EPaCSecretNotFound) {
		t.Error("unexpected boerror type for non-NotFound error")
	}
}

func TestLookupGHAppSecretOldModel_NotFoundReturnsBoError(t *testing.T) {
	s := newScheme()

	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	reconciler := &ComponentBuildReconcilerOldModel{
		Client:        fakeClient,
		Scheme:        s,
		EventRecorder: newFakeEventRecorder(),
	}

	_, err := reconciler.lookupGHAppSecret(context.Background())
	if err == nil {
		t.Fatal("expected error when secret is not found, got nil")
	}
	if !boerrors.IsBuildOpError(err, boerrors.EPaCSecretNotFound) {
		t.Errorf("expected EPaCSecretNotFound boerror, got: %v", err)
	}
}

// --- Tests for getWebhookSecretKeyForComponent ---

func TestGetWebhookSecretKeyForComponent_SpecialChars(t *testing.T) {
	component := compv1alpha1.Component{
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{
				GitURL: "https://github.com/org/repo",
			},
		},
	}
	key := getWebhookSecretKeyForComponent(component)
	if key == "" {
		t.Fatal("expected non-empty key")
	}
	// The URL contains "://" which should be replaced with underscores.
	if key == "https://github.com/org/repo" {
		t.Error("expected special chars to be replaced")
	}
}

// --- Test for ensureWebhookSecret NotFound+Create+Recursive call (success path) ---

func TestEnsureWebhookSecret_NotFoundCreatesAndRecurses(t *testing.T) {
	s := newScheme()

	// No webhook secret exists, so Get returns NotFound.
	// Create should succeed, then the recursive call should find the secret and add the key.
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	reconciler := &ComponentBuildReconciler{Client: fakeClient, Scheme: s}

	component := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "comp", Namespace: "ns1"},
		Spec: compv1alpha1.ComponentSpec{
			Source: compv1alpha1.ComponentSource{GitURL: "https://github.com/org/repo"},
		},
	}

	result, err := reconciler.ensureWebhookSecret(context.Background(), component)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty webhook secret string")
	}
	if len(result) != 40 {
		t.Errorf("expected 40-character hex string, got length %d", len(result))
	}

	// Verify the secret was persisted.
	stored := &corev1.Secret{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      pipelinesAsCodeWebhooksSecretName,
		Namespace: "ns1",
	}, stored)
	if err != nil {
		t.Fatalf("secret was not persisted: %v", err)
	}
	secretKey := getWebhookSecretKeyForComponent(*component)
	if _, exists := stored.Data[secretKey]; !exists {
		t.Error("component webhook secret key was not stored")
	}
}

// --- Test that ensures lookupGHAppSecret returns the secret directly when it exists (for NotFound branch as second condition) ---

func TestLookupGHAppSecret_NotFoundIsStatusError(t *testing.T) {
	s := newScheme()

	// Use a non-404 status error to ensure it's handled differently.
	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return &errors.StatusError{
					ErrStatus: metav1.Status{
						Status:  metav1.StatusFailure,
						Code:    403,
						Reason:  metav1.StatusReasonForbidden,
						Message: "forbidden",
					},
				}
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).Build()
	reconciler := &ComponentBuildReconciler{
		Client:        fakeClient,
		Scheme:        s,
		EventRecorder: newFakeEventRecorder(),
	}

	_, err := reconciler.lookupGHAppSecret(context.Background())
	if err == nil {
		t.Fatal("expected error for forbidden access, got nil")
	}
	// Should NOT be a boerror EPaCSecretNotFound for non-404 errors.
	if boerrors.IsBuildOpError(err, boerrors.EPaCSecretNotFound) {
		t.Error("403 Forbidden should not be treated as EPaCSecretNotFound")
	}
}
