/*
Copyright 2022-2025 Red Hat, Inc.

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
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	uberzap "go.uber.org/zap"
	uberzapcore "go.uber.org/zap/zapcore"

	controllers "github.com/konflux-ci/build-service/internal/controller"
	"github.com/konflux-ci/build-service/pkg/bometrics"
	"github.com/konflux-ci/build-service/pkg/k8s"
	l "github.com/konflux-ci/build-service/pkg/logs"
	pacwebhook "github.com/konflux-ci/build-service/pkg/pacwebhook"

	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	imagerepositoryapi "github.com/konflux-ci/image-controller/api/v1alpha1"
	releaseapi "github.com/konflux-ci/release-service/api/v1alpha1"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(appstudiov1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	var webhookConfigPath string

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&webhookConfigPath, "webhook-config-path", "", "Path to a file that contains webhook configurations")

	zapOpts := zap.Options{
		TimeEncoder: uberzapcore.ISO8601TimeEncoder,
		ZapOpts:     []uberzap.Option{uberzap.WithCaller(true)},
	}
	zapOpts.BindFlags(flag.CommandLine)

	klog.InitFlags(flag.CommandLine)

	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
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
	if err := releaseapi.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add pipelinesascode api to the scheme")
		os.Exit(1)
	}
	if err := imagerepositoryapi.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add pipelinesascode api to the scheme")
		os.Exit(1)
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		// TLSOpts is used to allow configuring the TLS config used for the server. If certificates are
		// not provided, self-signed certificates will be generated by default. This option is not recommended for
		// production environments as self-signed certificates do not offer the same level of trust and security
		// as certificates issued by a trusted Certificate Authority (CA). The primary risk is potentially allowing
		// unauthorized access to sensitive metrics data. Consider replacing with CertDir, CertName, and KeyName
		// to provide certificates, ensuring the server communicates using trusted and secure certificates.
		TLSOpts: tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	clientOpts := client.Options{
		Cache: &client.CacheOptions{
			DisableFor: getCacheExcludedObjectsTypes(),
		},
	}

	restConfig := ctrl.GetConfigOrDie()
	ensureRequiredAPIGroupsAndResourcesExist(restConfig)

	leaseDuration := 60 * time.Second
	renewDeadline := 35 * time.Second

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Client:                        clientOpts,
		Scheme:                        scheme,
		Cache:                         getCacheOptions(),
		Metrics:                       metricsServerOptions,
		WebhookServer:                 webhookServer,
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                enableLeaderElection,
		LeaderElectionID:              "5483be8f.redhat.com",
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &leaseDuration,
		RenewDeadline:                 &renewDeadline,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ensureBuildPipelineClusterRoleExist(mgr.GetClient())

	webhookConfig, err := pacwebhook.LoadMappingFromFile(webhookConfigPath, os.ReadFile)
	if err != nil {
		setupLog.Error(err, "Failed to load webhook config file", "path", webhookConfigPath)
		os.Exit(1)
	}
	if err = (&controllers.ComponentBuildReconciler{
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		EventRecorder:      mgr.GetEventRecorderFor("ComponentOnboarding"),
		WebhookURLLoader:   pacwebhook.NewConfigWebhookURLLoader(webhookConfig),
		CredentialProvider: k8s.NewGitCredentialProvider(mgr.GetClient()),
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

	if err = (&controllers.ComponentDependencyUpdateReconciler{
		Client:                       mgr.GetClient(),
		ApiReader:                    mgr.GetAPIReader(),
		Scheme:                       mgr.GetScheme(),
		EventRecorder:                mgr.GetEventRecorderFor("ComponentDependencyUpdateReconciler"),
		ComponentDependenciesUpdater: *controllers.NewComponentDependenciesUpdater(mgr.GetClient(), mgr.GetScheme(), mgr.GetEventRecorderFor("ComponentDependencyUpdateReconciler")),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ComponentDependencyUpdateReconciler")
		os.Exit(1)
	}

	// TODO delete the controller after migration to the new dedicated to build Service Account.
	if err = (&controllers.AppstudioPipelineServiceAccountWatcherReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AppstudioPipelineServiceAccountWatcher")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

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

	ctx := ctrl.SetupSignalHandler()
	buildMetrics := bometrics.NewBuildMetrics([]bometrics.AvailabilityProbe{bometrics.NewGithubAppAvailabilityProbe(mgr.GetClient())})
	if err := buildMetrics.InitMetrics(metrics.Registry); err != nil {
		setupLog.Error(err, "unable to initialize metrics")
		os.Exit(1)
	}
	buildMetrics.StartAvailabilityProbes(ctx)

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
		&rbacv1.ClusterRole{},
	}
}

func getCacheOptions() cache.Options {
	componentPipelineRunRequirement, err := labels.NewRequirement(controllers.ComponentNameLabelName, selection.Exists, []string{})
	if err != nil {
		// With valid arguments for the requirement above, the error is always nil.
		panic(err)
	}
	appStudioComponentPipelineRunSelector := labels.NewSelector().Add(*componentPipelineRunRequirement)

	return cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&tektonapi.PipelineRun{}: {
				Label: appStudioComponentPipelineRunSelector,
			},
			&releaseapi.ReleasePlanAdmission{}: {},
		},
	}
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

	discoveryClient := discovery.NewDiscoveryClientForConfigOrDie(restConfig)

	delay := 5 * time.Second
	attempts := 60
	for i := 0; i < attempts; i++ {
		if isRequiredAPIGroupsAndResourcesExist(requiredGroupsAndResources, discoveryClient) {
			return
		}
		time.Sleep(delay)
	}

	setupLog.Error(fmt.Errorf("failed to start operator"), "timed out waiting for required API Groups and Resources")
	os.Exit(1)
}

func isRequiredAPIGroupsAndResourcesExist(requiredGroupsAndResources map[string][]string, discoveryClient *discovery.DiscoveryClient) bool {
	apiGroups, apiResources, err := discoveryClient.ServerGroupsAndResources()
	if err != nil {
		setupLog.Error(err, "failed to get ServerGroups using discovery client")
		return false
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
					return false
				}
				// All API resources from the group is available
				continue NextGroup
			}
		}
		// Required group is not found in the list of available groups
		setupLog.Info(fmt.Sprintf("Waiting for %s API Group", requiredGroupName))
		return false
	}

	return true
}

// Makes sure that Cluster Role that defines permissins for build pipelines exists in the cluster.
// Otherwise no build can be done and there is no point in starting the operator.
func ensureBuildPipelineClusterRoleExist(client client.Client) {
	buildPipelinesRunnerClusterRole := &rbacv1.ClusterRole{}
	buildPipelinesRunnerClusterRoleKey := types.NamespacedName{Name: controllers.BuildPipelineClusterRoleName}

	delay := 5 * time.Second
	attempts := 60
	for i := 0; i < attempts; i++ {
		if err := client.Get(context.TODO(), buildPipelinesRunnerClusterRoleKey, buildPipelinesRunnerClusterRole); err == nil {
			return
		} else {
			if errors.IsNotFound(err) {
				setupLog.Info(fmt.Sprintf("Waiting for %s Cluster Role", controllers.BuildPipelineClusterRoleName))
			} else {
				setupLog.Info(fmt.Sprintf("failed to get %s Cluster Role: %s", controllers.BuildPipelineClusterRoleName, err.Error()))
			}
		}
		time.Sleep(delay)
	}

	setupLog.Error(fmt.Errorf("failed to start operator"), fmt.Sprintf("timed out waiting for %s Cluster Role", controllers.BuildPipelineClusterRoleName))
	os.Exit(1)
}
