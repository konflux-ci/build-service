package common

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Creates a new secret in a specified namespace
func (s *SuiteController) CreateSecret(ns string, secret *corev1.Secret) (*corev1.Secret, error) {
	return s.KubeInterface().CoreV1().Secrets(ns).Create(context.Background(), secret, metav1.CreateOptions{})
}

// Check if a secret exists, return secret and error
func (s *SuiteController) GetSecret(ns string, name string) (*corev1.Secret, error) {
	return s.KubeInterface().CoreV1().Secrets(ns).Get(context.Background(), name, metav1.GetOptions{})
}

// Update a secret in a specified namespace
func (s *SuiteController) UpdateSecret(ns string, secret *corev1.Secret) (*corev1.Secret, error) {
	return s.KubeInterface().CoreV1().Secrets(ns).Update(context.Background(), secret, metav1.UpdateOptions{})
}

// Delete a secret in a specified namespace
func (s *SuiteController) DeleteSecret(ns string, name string) error {
	return s.KubeInterface().CoreV1().Secrets(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
}
