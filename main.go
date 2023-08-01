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
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	uberzap "go.uber.org/zap"
	uberzapcore "go.uber.org/zap/zapcore"
	"k8s.io/client-go/discovery"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/build-service/api/v1alpha1"
	"github.com/redhat-appstudio/build-service/controllers"
	l "github.com/redhat-appstudio/build-service/pkg/logs"
	appstudiospiapiv1beta1 "github.com/redhat-appstudio/service-provider-integration-operator/api/v1beta1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog logr.Logger
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(appstudiov1alpha1.AddToScheme(scheme))
	utilruntime.Must(appstudioredhatcomv1alpha1.AddToScheme(scheme))
	utilruntime.Must(appstudiospiapiv1beta1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	zapOpts := zap.Options{
		TimeEncoder: uberzapcore.ISO8601TimeEncoder,
		ZapOpts:     []uberzap.Option{uberzap.WithCaller(true)},
	}
	zapOpts.BindFlags(flag.CommandLine)
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	setupLog = ctrl.Log.WithName("setup")
	klog.SetLogger(setupLog)

	if err := routev1.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add openshift route api to the scheme")
		os.Exit(1)
	}

	if err := tektonapi.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add tekton api to the scheme")
		os.Exit(1)
	}

	if err := pacv1alpha1.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add pipelinesascode api to the scheme")
		os.Exit(1)
	}

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
		NewCache:               cache.BuilderWithOptions(*cacheOptions),
	}
	restConfig := ctrl.GetConfigOrDie()

	ensureRequiredAPIGroupsAndResourcesExist(restConfig)

	mgr, err := ctrl.NewManager(restConfig, options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.ComponentBuildReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		EventRecorder: mgr.GetEventRecorderFor("ComponentOnboarding"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ComponentOnboarding")
		os.Exit(1)
	}

	if err = (&controllers.PaCPipelineRunPrunerReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		EventRecorder: mgr.GetEventRecorderFor("PaCPipelineRunPruner"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PaCPipelineRunPruner")
		os.Exit(1)
	}

	if err = (&controllers.GitTektonResourcesRenovater{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		EventRecorder: mgr.GetEventRecorderFor("GitTektonResourcesRenovater"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "GitTektonResourcesRenovater")
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

	if prImageExpiration := os.Getenv(controllers.PipelineRunOnPRExpirationEnvVar); prImageExpiration != "" {
		validExpiration, _ := regexp.Match("^[1-9][0-9]{0,2}[hdw]$", []byte(prImageExpiration))
		if !validExpiration {
			setupLog.Info(fmt.Sprintf("invalid expiration '%s' in %s environment variable, using default %s",
				prImageExpiration, controllers.PipelineRunOnPRExpirationEnvVar, controllers.PipelineRunOnPRExpirationDefault), l.Audit, "true")
			if err := os.Setenv(controllers.PipelineRunOnPRExpirationEnvVar, controllers.PipelineRunOnPRExpirationDefault); err != nil {
				setupLog.Error(err, "unable to set default PipelineRun expiration environment variable")
				os.Exit(1)
			}
		}
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
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
		&tektonapi.PipelineRun{}: cache.ObjectSelector{
			Label: appStudioComponentPipelineRunSelector,
		},
	}

	return &cache.Options{
		SelectorsByObject: selectors,
	}, nil
}

func ensureRequiredAPIGroupsAndResourcesExist(restConfig *rest.Config) {
	// Do not start the operator until all of the below is available
	requiredGroupsAndResources := map[string][]string{
		"tekton.dev": {
			"pipelineruns",
		},
		"appstudio.redhat.com": {
			"components",
			"applications",
		},
		"pipelinesascode.tekton.dev": {
			"repositories",
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
