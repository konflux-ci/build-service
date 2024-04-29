package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/redhat-appstudio/build-service/pkg/git/credentials"
)

func TestSecretMatching(t *testing.T) {
	tests := []struct {
		testcase string
		in       []corev1.Secret
		repo     string
		expected string
	}{
		{
			testcase: "Direct match vs nothing",
			repo:     "test/repo",
			in: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "secret1",
						Annotations: map[string]string{
							ScmSecretRepositoryAnnotation: "test/repo",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "secret2",
					},
				},
			},
			expected: "secret1",
		},
		{
			testcase: "Wildcard match vs nothing",
			repo:     "test/repo",
			in: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "secret1",
						Annotations: map[string]string{
							ScmSecretRepositoryAnnotation: "/test/*, /foo/bar",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "secret2",
					},
				},
			},
			expected: "secret1",
		},
		{
			testcase: "Direct vs wildcard match",
			repo:     "test/repo",
			in: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "secret1",
						Annotations: map[string]string{
							ScmSecretRepositoryAnnotation: "test/repo, foo/bar",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "secret2",
						Annotations: map[string]string{
							ScmSecretRepositoryAnnotation: "test/*",
						},
					},
				},
			},
			expected: "secret1",
		},
		{
			testcase: "Wildcard better match",
			repo:     "test/repo",
			in: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "secret1",
						Annotations: map[string]string{
							ScmSecretRepositoryAnnotation: "test/*",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "secret2",
						Annotations: map[string]string{
							ScmSecretRepositoryAnnotation: "test/repo/*",
						},
					},
				},
			},
			expected: "secret2",
		},
	}
	for _, tt := range tests {
		t.Run("intersection test", func(t *testing.T) {
			got := bestMatchingSecret(context.Background(), tt.repo, tt.in)
			if got.Name != tt.expected {
				t.Errorf("Got secret mathed %s but expected is %s", got.Name, tt.expected)
			}
		})
	}
}
