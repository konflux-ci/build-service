package kubernetes

import (
	applicationApi "github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	imageRepositoryApi "github.com/konflux-ci/image-controller/api/konflux/v1alpha1"
	tekton "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

type CustomClient struct {
	kubeClient *kubernetes.Clientset
	crClient   crclient.Client
}

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(imageRepositoryApi.AddToScheme(scheme))
	utilruntime.Must(applicationApi.AddToScheme(scheme))
	utilruntime.Must(tekton.AddToScheme(scheme))
}

// KubeInterface returns the clientset for Kubernetes upstream.
func (c *CustomClient) KubeInterface() kubernetes.Interface {
	return c.kubeClient
}

// KubeRest returns a rest client to perform CRUD operations on Kubernetes objects
func (c *CustomClient) KubeRest() crclient.Client {
	return c.crClient
}

// Creates a kubernetes client from default kubeconfig. Will take it from KUBECONFIG env if it is defined and if in case is not defined
// will create the client from $HOME/.kube/config
func NewAdminKubernetesClient() (*CustomClient, error) {
	adminKubeconfig, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	clientSets, err := createClientSetsFromConfig(adminKubeconfig)
	if err != nil {
		return nil, err
	}

	crClient, err := crclient.New(adminKubeconfig, crclient.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, err
	}

	return &CustomClient{
		kubeClient: clientSets.kubeClient,
		crClient:   crClient,
	}, nil
}

func createClientSetsFromConfig(cfg *rest.Config) (*CustomClient, error) {
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &CustomClient{
		kubeClient: client,
	}, nil
}
