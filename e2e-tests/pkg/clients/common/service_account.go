package common

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *SuiteController) GetServiceAccount(saName, namespace string) (*corev1.ServiceAccount, error) {
	return s.KubeInterface().CoreV1().ServiceAccounts(namespace).Get(context.Background(), saName, metav1.GetOptions{})
}

// IsSecretLinkedToServiceAccount checks whether a secret is linked to a service account
func (s *SuiteController) IsSecretLinkedToServiceAccount(namespace, serviceAccountName, secretName string) (bool, error) {
	serviceAccount, err := s.GetServiceAccount(serviceAccountName, namespace)
	if err != nil {
		return false, err
	}
	secretLinked := false
	for _, secretRef := range serviceAccount.Secrets {
		if secretRef.Name == secretName {
			fmt.Printf("found secret %s is linked to service account %s\n", secretName, serviceAccountName)
			secretLinked = true
			break
		}
	}
	return secretLinked, nil
}

// UnlinkSecretFromServiceAccount unlinks secret from service account
func (s *SuiteController) UnlinkSecretFromServiceAccount(namespace, secretName, serviceAccount string, rmImagePullSecrets bool) error {
	serviceAccountObject, err := s.KubeInterface().CoreV1().ServiceAccounts(namespace).Get(context.Background(), serviceAccount, metav1.GetOptions{})
	if err != nil {
		return err
	}

	for index, secret := range serviceAccountObject.Secrets {
		if secret.Name == secretName {
			serviceAccountObject.Secrets = append(serviceAccountObject.Secrets[:index], serviceAccountObject.Secrets[index+1:]...)
			break
		}
	}

	if rmImagePullSecrets {
		for index, secret := range serviceAccountObject.ImagePullSecrets {
			if secret.Name == secretName {
				serviceAccountObject.ImagePullSecrets = append(serviceAccountObject.ImagePullSecrets[:index], serviceAccountObject.ImagePullSecrets[index+1:]...)
				break
			}
		}
	}
	_, err = s.KubeInterface().CoreV1().ServiceAccounts(namespace).Update(context.Background(), serviceAccountObject, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}
