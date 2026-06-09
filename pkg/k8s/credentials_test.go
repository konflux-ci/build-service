package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/konflux-ci/build-service/pkg/git/credentials"
)

func TestOrderedPrefixIntersection(t *testing.T) {
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
			got := orderedPrefixIntersection(tt.in1, tt.in2)
			if got != tt.intersection {
				t.Errorf("Got slice intersection %d but expected length is %d", got, tt.intersection)
			}
		})
	}
}

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
