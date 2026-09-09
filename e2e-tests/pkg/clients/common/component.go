package common

import (
	"context"
	"fmt"
	"time"

	"github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	applicationApi "github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/utils"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Method to create a component in the kubernetes clusters.
func (s *SuiteController) CreateComponent(componentObj *applicationApi.Component) (*applicationApi.Component, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()
	if err := s.KubeRest().Create(ctx, componentObj); err != nil {
		return nil, err
	}
	return componentObj, nil
}

// GetComponent reads and returns the component
func (s *SuiteController) GetComponent(componentName, namespace string) (applicationApi.Component, error) {
	namespacedName := types.NamespacedName{
		Name:      componentName,
		Namespace: namespace,
	}
	component := applicationApi.Component{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &component)
	if err != nil {
		return component, err
	}
	return component, err
}

// DeleteComponent removes the Component object
func (s *SuiteController) DeleteComponent(name, namespace string) error {
	componentObj := v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := s.KubeRest().Delete(context.Background(), &componentObj); err != nil {
		if !k8sErrors.IsNotFound(err) {
			return fmt.Errorf("error deleting component: %+v", err)
		}
	}
	return nil
}

// WaitForImageRepositoryToBeReady waits for the image repository status to be in ready state
func (s *SuiteController) WaitForComponentVersionOnboardingToSucceed(componentName, namespace, testVersionName string) error {
	namespacedName := types.NamespacedName{
		Name:      componentName,
		Namespace: namespace,
	}
	componentObj := v1alpha1.Component{}

	err := utils.WaitUntil(func() (done bool, err error) {
		if err := s.KubeRest().Get(context.Background(), namespacedName, &componentObj); err != nil {
			fmt.Printf("failed to get component %s with error: %v\n", componentName, err)
			return false, nil
		}
		for _, version := range componentObj.Status.Versions {
			if version.Name == testVersionName && version.OnboardingStatus == "succeeded" {
				return true, nil
			}
			fmt.Printf("current status for version: %v\n", version)
		}
		return false, nil
	}, 2*time.Minute)

	return err
}
