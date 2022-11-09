/*
Copyright 2022.

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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"k8s.io/client-go/discovery"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"

	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/kcp"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/build-service/controllers"
	taskrunapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	triggersapi "github.com/tektoncd/triggers/pkg/apis/triggers/v1alpha1"

	apisv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/apis/v1alpha1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(appstudiov1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get
// +kubebuilder:rbac:groups="apis.kcp.dev",resources=apiexports,verbs=get;list;watch
// +kubebuilder:rbac:groups="apis.kcp.dev",resources=apiexports/content,verbs=*

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var apiExportName string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&apiExportName, "api-export-name", "build-service-apiexport", "The name of the APIExport.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if err := routev1.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add openshift route api to the scheme")
		os.Exit(1)
	}

	if err := triggersapi.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add triggers api to the scheme")
		os.Exit(1)
	}

	if err := taskrunapi.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add triggers api to the scheme")
		os.Exit(1)
	}

	if err := pacv1alpha1.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add pipelinesascode api to the scheme")
		os.Exit(1)
	}

	var localClient client.Client
	var mgr ctrl.Manager

	cacheOptions, err := getCacheFuncOptions()
	if err != nil {
		setupLog.Error(err, "unable to create cache options")
		os.Exit(1)
	}

	options := ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "5483be8f.redhat.com",
		ClientDisableCacheFor:  getCacheExcludedObjectsTypes(),
	}
	restConfig := ctrl.GetConfigOrDie()

	ctx := ctrl.SetupSignalHandler()

	if kcpAPIsGroupPresent(restConfig) {
		setupLog.Info("Looking up virtual workspace URL")
		cfg, err := restConfigForAPIExport(ctx, restConfig, apiExportName)
		if err != nil {
			setupLog.Error(err, "error looking up virtual workspace URL")
		}

		setupLog.Info("Using virtual workspace URL", "url", cfg.Host)

		options.NewCache = kcp.ClusterAwareBuilderWithOptions(*cacheOptions)
		options.LeaderElectionConfig = restConfig
		mgr, err = kcp.NewClusterAwareManager(cfg, options)
		if err != nil {
			setupLog.Error(err, "unable to start cluster aware manager")
			os.Exit(1)
		}

		// Local client is needed to access the workspace where operator deployment is
		localClientScheme := runtime.NewScheme()
		if err := clientgoscheme.AddToScheme(localClientScheme); err != nil {
			setupLog.Error(err, "error adding standard types local client sceme")
		}
		localClient, err = client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: localClientScheme})
		if err != nil {
			setupLog.Error(err, "error creating local client")
		}
	} else {
		setupLog.Info("The apis.kcp.dev group is not present - creating standard manager")

		ensureRequiredAPIGroupsAndResourcesExist(restConfig)

		options.NewCache = cache.BuilderWithOptions(*cacheOptions)
		mgr, err = ctrl.NewManager(restConfig, options)
		if err != nil {
			setupLog.Error(err, "unable to start manager")
			os.Exit(1)
		}

		localClient = mgr.GetClient()
	}

	if err = (&controllers.ComponentBuildReconciler{
		Client:        mgr.GetClient(),
		LocalClient:   localClient,
		Scheme:        mgr.GetScheme(),
		Log:           ctrl.Log.WithName("controllers").WithName("ComponentOnboarding"),
		EventRecorder: mgr.GetEventRecorderFor("ComponentOnboarding"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ComponentOnboarding")
		os.Exit(1)
	}

	if err = (&controllers.ComponentImageReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Log:           ctrl.Log.WithName("controllers").WithName("ComponentImage"),
		EventRecorder: mgr.GetEventRecorderFor("ComponentImage"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ComponentImage")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getCacheExcludedObjectsTypes() []client.Object {
	return []client.Object{
		&corev1.Secret{},
		&corev1.ConfigMap{},
	}
}

func getCacheFuncOptions() (*cache.Options, error) {
	componentPipelineRunRequirement, err := labels.NewRequirement(controllers.ComponentNameLabelName, selection.Exists, []string{})
	if err != nil {
		return nil, err
	}
	appStudioComponentPipelineRunSelector := labels.NewSelector().Add(*componentPipelineRunRequirement)

	selectors := cache.SelectorsByObject{
		&taskrunapi.PipelineRun{}: {
			Label: appStudioComponentPipelineRunSelector,
		},
	}

	return &cache.Options{
		SelectorsByObject: selectors,
	}, nil
}

// restConfigForAPIExport returns a *rest.Config properly configured to communicate with the endpoint for the
// APIExport's virtual workspace.
func restConfigForAPIExport(ctx context.Context, cfg *rest.Config, apiExportName string) (*rest.Config, error) {
	scheme := runtime.NewScheme()
	if err := apisv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("error adding apis.kcp.dev/v1alpha1 to scheme: %w", err)
	}

	apiExportClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("error creating APIExport client: %w", err)
	}

	var apiExport apisv1alpha1.APIExport

	if apiExportName != "" {
		if err := apiExportClient.Get(ctx, types.NamespacedName{Name: apiExportName}, &apiExport); err != nil {
			return nil, fmt.Errorf("error getting APIExport %q: %w", apiExportName, err)
		}
	} else {
		setupLog.Info("api-export-name is empty - listing")
		exports := &apisv1alpha1.APIExportList{}
		if err := apiExportClient.List(ctx, exports); err != nil {
			return nil, fmt.Errorf("error listing APIExports: %w", err)
		}
		if len(exports.Items) == 0 {
			return nil, fmt.Errorf("no APIExport found")
		}
		if len(exports.Items) > 1 {
			return nil, fmt.Errorf("more than one APIExport found")
		}
		apiExport = exports.Items[0]
	}

	if len(apiExport.Status.VirtualWorkspaces) < 1 {
		return nil, fmt.Errorf("APIExport %q status.virtualWorkspaces is empty", apiExportName)
	}

	cfg = rest.CopyConfig(cfg)
	cfg.Host = apiExport.Status.VirtualWorkspaces[0].URL

	return cfg, nil
}

func kcpAPIsGroupPresent(restConfig *rest.Config) bool {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		setupLog.Error(err, "failed to create discovery client")
		os.Exit(1)
	}
	apiGroupList, err := discoveryClient.ServerGroups()
	if err != nil {
		setupLog.Error(err, "failed to get server groups")
		os.Exit(1)
	}

	for _, group := range apiGroupList.Groups {
		if group.Name == apisv1alpha1.SchemeGroupVersion.Group {
			for _, version := range group.Versions {
				if version.Version == apisv1alpha1.SchemeGroupVersion.Version {
					return true
				}
			}
		}
	}
	return false
}

func ensureRequiredAPIGroupsAndResourcesExist(restConfig *rest.Config) {
	// Do not start the operator until all of the below is available
	requiredGroupsAndResources := map[string][]string{
		"tekton.dev": {
			"pipelineruns",
			"taskruns",
		},
		"appstudio.redhat.com": {
			"components",
			"applications",
		},
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		setupLog.Error(err, "failed to create discovery client")
		os.Exit(1)
	}

	if err := wait.PollImmediate(time.Second*5, time.Minute*5, func() (done bool, err error) {
		apiGroups, apiResources, err := discoveryClient.ServerGroupsAndResources()
		if err != nil {
			setupLog.Error(err, "failed to get ServerGroups using discovery client")
			return false, nil
		}

	NextGroup:
		for requiredGroupName, requiredGroupResources := range requiredGroupsAndResources {
			// Search for the given group in all groups list
			for _, group := range apiGroups {
				if group.Name == requiredGroupName {
					// Required group exists
					// Check for required resources of the group
				NextResource:
					for _, requiredGroupResource := range requiredGroupResources {
						for _, apiResource := range apiResources {
							groupName := strings.Split(apiResource.GroupVersion, "/")[0]
							if groupName == requiredGroupName {
								for _, apiResourceInGroup := range apiResource.APIResources {
									if apiResourceInGroup.Name == requiredGroupResource {
										continue NextResource
									}
								}
								// The resource is not found in this version of the group
							}
						}
						// Required API resource is not found in the list of available API resources
						setupLog.Info(fmt.Sprintf("Waiting for %s API Resourse under %s API Group", requiredGroupResource, requiredGroupName))
						return false, nil
					}
					// All API resources from the group is available
					continue NextGroup
				}
			}
			// Required group is not found in the list of available groups
			setupLog.Info(fmt.Sprintf("Waiting for %s API Group", requiredGroupName))
			return false, nil
		}

		return true, nil
	}); err != nil {
		setupLog.Error(err, "timed out waiting for required API Groups and Resources")
		os.Exit(1)
	}
}
