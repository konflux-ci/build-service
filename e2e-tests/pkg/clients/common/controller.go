package common

import (
	"github.com/konflux-ci/build-service/e2e-tests/pkg/clients/git"
	kubeCl "github.com/konflux-ci/build-service/e2e-tests/pkg/clients/kubernetes"
)

// SuiteController gives access to the kubernetes cluster and to the git providers used by the tests.
type SuiteController struct {
	*kubeCl.CustomClient
	// GitClients holds a provider agnostic client per configured git provider.
	GitClients git.Clients
}

func NewSuiteController(kubeC *kubeCl.CustomClient, gitClients git.Clients) *SuiteController {
	return &SuiteController{
		CustomClient: kubeC,
		GitClients:   gitClients,
	}
}
