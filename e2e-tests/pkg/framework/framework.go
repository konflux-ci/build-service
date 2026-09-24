package framework

import (
	"fmt"
	"os"

	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/common"
	kubeCl "github.com/konflux-ci/build-service/e2e-tests/pkg/clients/kubernetes"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/constants"
)

type ControllerHub struct {
	CommonController *common.SuiteController
}

type Framework struct {
	AsKubeAdmin   *ControllerHub
	TestNamespace string
}

func NewFramework(namespaceName string) (*Framework, error) {
	var err error
	var asAdmin *ControllerHub

	if namespaceName == "" {
		return nil, fmt.Errorf("namespaceName cannot be empty when initializing a new framework instance")
	}

	client, err := kubeCl.NewAdminKubernetesClient()
	if err != nil {
		return nil, err
	}

	asAdmin, err = InitControllerHub(client)
	if err != nil {
		return nil, fmt.Errorf("error when initializing appstudio hub controllers for admin user: %v", err)
	}

	// Use env E2E_NAMESPACE if defined
	nsName := os.Getenv(constants.E2E_NAMESPACE_ENV)
	if nsName == "" {
		nsName = namespaceName

		_, err := asAdmin.CommonController.CreateTestNamespace(namespaceName)
		if err != nil {
			return nil, fmt.Errorf("failed to create test namespace %s: %+v", nsName, err)
		}
	}

	return &Framework{
		AsKubeAdmin:   asAdmin,
		TestNamespace: nsName,
	}, nil
}

func InitControllerHub(cc *kubeCl.CustomClient) (*ControllerHub, error) {
	// Initialize Common controller
	commonCtrl, err := common.NewSuiteController(cc)
	if err != nil {
		return nil, err
	}

	return &ControllerHub{
		CommonController: commonCtrl,
	}, nil
}
