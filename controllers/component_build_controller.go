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

package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	appstudiov1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	"github.com/redhat-appstudio/application-service/pkg/devfile"
	buildappstudiov1alpha1 "github.com/redhat-appstudio/build-service/api/v1alpha1"
	"github.com/redhat-appstudio/build-service/pkg/github"
	"github.com/redhat-appstudio/build-service/pkg/gitlab"
	pipelineselector "github.com/redhat-appstudio/build-service/pkg/pipeline-selector"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	oci "github.com/tektoncd/pipeline/pkg/remote/oci"
)

const (
	InitialBuildAnnotationName = "com.redhat.appstudio/component-initial-build-processed"
	ApplicationNameLabelName   = "appstudio.openshift.io/application"
	ComponentNameLabelName     = "appstudio.openshift.io/component"
	PartOfLabelName            = "app.kubernetes.io/part-of"
	PartOfAppStudioLabelValue  = "appstudio"

	PipelineRunOnPushSuffix    = "-on-push"
	PipelineRunOnPRSuffix      = "-on-pull-request"
	PipelineRunOnPushFilename  = "push.yaml"
	PipelineRunOnPRFilename    = "pull-request.yaml"
	pipelinesAsCodeNamespace   = "pipelines-as-code"
	pipelinesAsCodeRouteName   = "pipelines-as-code-controller"
	pipelinesAsCodeRouteEnvVar = "PAC_WEBHOOK_URL"

	buildPipelineServiceAccountName   = "pipeline"
	buildServiceNamespaceName         = "build-service"
	buildPipelineSelectorResourceName = "build-pipeline-selector"
	defaultPipelineName               = "docker-build"

	metricsNamespace = "redhat_appstudio"
	metricsSubsystem = "buildservice"
)

var (
	initialBuildPipelineCreationTimeMetric      prometheus.Histogram
	pipelinesAsCodeComponentProvisionTimeMetric prometheus.Histogram
)

func initMetrics() error {
	buckets := getProvisionTimeMetricsBuckets()

	initialBuildPipelineCreationTimeMetric = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Buckets:   buckets,
		Name:      "initial_build_pipeline_creation_time",
		Help:      "The time in seconds spent from the moment of Component creation till the initial build pipeline submition.",
	})
	pipelinesAsCodeComponentProvisionTimeMetric = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Buckets:   buckets,
		Name:      "PaC_configuration_time",
		Help:      "The time in seconds spent from the moment of Component creation till Pipelines-as-Code configuration done in the Component source repository.",
	})

	if err := metrics.Registry.Register(initialBuildPipelineCreationTimeMetric); err != nil {
		return fmt.Errorf("failed to register the initial_build_pipeline_creation_time metric: %w", err)
	}
	if err := metrics.Registry.Register(pipelinesAsCodeComponentProvisionTimeMetric); err != nil {
		return fmt.Errorf("failed to register the PaC_configuration_time metric: %w", err)
	}

	return nil
}

func getProvisionTimeMetricsBuckets() []float64 {
	return []float64{5, 10, 15, 20, 30, 60, 120, 300}
}

// ComponentBuildReconciler watches AppStudio Component object in order to submit builds
type ComponentBuildReconciler struct {
	Client        client.Client
	Scheme        *runtime.Scheme
	Log           logr.Logger
	EventRecorder record.EventRecorder
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := initMetrics(); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&appstudiov1alpha1.Component{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return true
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		})).
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=buildpipelineselectors,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=create
//+kubebuilder:rbac:groups=pipelinesascode.tekton.dev,resources=repositories,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;patch;update
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *ComponentBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ComponentOnboarding", req.NamespacedName)

	// Fetch the Component instance
	var component appstudiov1alpha1.Component
	err := r.Client.Get(ctx, req.NamespacedName, &component)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	if component.Spec.ContainerImage == "" {
		// Expect that ContainerImage is set to default value if the field left empty by user.
		log.Info("Waiting for ContainerImage to be set")
		return ctrl.Result{}, nil
	}

	// Do not run any builds for any container-image components
	if component.Spec.ContainerImage != "" && (component.Spec.Source.GitSource == nil || component.Spec.Source.GitSource.URL == "") {
		log.Info(fmt.Sprintf("Nothing to do for container image component: %v", req.NamespacedName))
		return ctrl.Result{}, nil
	}

	if component.Status.Devfile == "" {
		// The component has been just created.
		// Component controller must set devfile model, wait for it.
		log.Info(fmt.Sprintf("Waiting for devfile model in component: %v", req.NamespacedName))
		// Do not requeue as after model update a new update event will trigger a new reconcile
		return ctrl.Result{}, nil
	}

	// Check initial build annotation to know if any work should be done for the component
	if len(component.Annotations) == 0 {
		component.Annotations = make(map[string]string)
	}
	if component.Annotations[InitialBuildAnnotationName] == "true" {
		// Initial build have already happend, nothing to do.
		return ctrl.Result{}, nil
	}
	// The initial build annotation is absent, onboarding of the component needed

	gitopsConfig := gitopsprepare.PrepareGitopsConfig(ctx, r.Client, component)
	if val, ok := component.Annotations[gitops.PaCAnnotation]; (ok && val == "1") || gitopsConfig.IsHACBS {
		// Use pipelines as code build
		log.Info("Pipelines as Code enabled")

		gitProvider, err := gitops.GetGitProvider(component)
		if err != nil {
			log.Error(err, "error detecting git provider")
			// Do not reconcile, because configuration must be fixed before it is possible to proceed.
			return ctrl.Result{}, nil
		}

		// Expect that the secret contains token for Pipelines as Code webhook configuration,
		// but under <git-provider>.token field. For example: github.token
		// Also it can contain github-private-key and github-application-id
		// in case GitHub Application is used instead of webhook.
		pacSecret := corev1.Secret{}
		pacSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: gitopsprepare.PipelinesAsCodeSecretName}
		if err := r.Client.Get(ctx, pacSecretKey, &pacSecret); err != nil {
			if !errors.IsNotFound(err) {
				log.Error(err, fmt.Sprintf("failed to get Pipelines as Code secret in %s namespace", component.Namespace))
				r.EventRecorder.Event(&pacSecret, "Warning", "ErrorReadingPaCSecret", err.Error())
				return ctrl.Result{}, err
			}

			// Fallback to the global configuration
			globalPaCSecretKey := types.NamespacedName{Namespace: buildServiceNamespaceName, Name: gitopsprepare.PipelinesAsCodeSecretName}
			if err := r.Client.Get(ctx, globalPaCSecretKey, &pacSecret); err != nil {
				if !errors.IsNotFound(err) {
					log.Error(err, fmt.Sprintf("failed to get Pipelines as Code secret in %s namespace", globalPaCSecretKey.Namespace))
					r.EventRecorder.Event(&pacSecret, "Warning", "ErrorReadingPaCSecret", err.Error())
					return ctrl.Result{}, err
				}
				log.Error(err, fmt.Sprintf("Pipelines as Code secret not found in %s namespace nor in %s", pacSecretKey.Namespace, globalPaCSecretKey.Namespace))
				r.EventRecorder.Event(&pacSecret, "Warning", "PaCSecretNotFound", err.Error())
				// Do not trigger a new reconcile. The PaC secret must be created first.
				return ctrl.Result{}, nil
			}

			if !gitops.IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
				// Webhook is used. We need to reference access token in the component namespace.
				// Copy global PaC configuration in component namespace
				localPaCSecret := &corev1.Secret{
					TypeMeta: pacSecret.TypeMeta,
					ObjectMeta: metav1.ObjectMeta{
						Name:      pacSecretKey.Name,
						Namespace: pacSecretKey.Namespace,
						Labels: map[string]string{
							PartOfLabelName: PartOfAppStudioLabelValue,
						},
					},
					Data: pacSecret.Data,
				}
				if err := r.Client.Create(ctx, localPaCSecret); err != nil {
					r.Log.Error(err, "failed to create local PaC configuration secret")
					return ctrl.Result{}, err
				}
			}
		}

		if err := validatePaCConfiguration(gitProvider, pacSecret.Data); err != nil {
			log.Error(err, "Invalid configuration in Pipelines as Code secret")
			r.EventRecorder.Event(&pacSecret, "Warning", "ErrorValidatingPaCSecret", err.Error())
			// Do not reconcile, because configuration must be fixed before it is possible to proceed.
			return ctrl.Result{}, nil
		}

		var webhookSecretString, webhookTargetUrl string
		if !gitops.IsPaCApplicationConfigured(gitProvider, pacSecret.Data) {
			// Generate webhook secret for the component git repository if not yet generated
			// and stores it in the corresponding k8s secret.
			webhookSecretString, err = r.ensureWebhookSecret(ctx, component)
			if err != nil {
				return ctrl.Result{}, err
			}

			// Obtain Pipelines as Code callback URL
			webhookTargetUrl = os.Getenv(pipelinesAsCodeRouteEnvVar)
			if webhookTargetUrl == "" {
				// The env variable is not set
				// Use the installed on the cluster Pipelines as Code
				webhookTargetUrl, err = r.getPaCRoutePublicUrl(ctx)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}

		if err := r.EnsurePaCRepository(ctx, component, pacSecret.Data); err != nil {
			return ctrl.Result{}, err
		}

		// Manage merge request for Pipelines as Code configuration
		mrUrl, err := r.ConfigureRepositoryForPaC(ctx, &component, pacSecret.Data, webhookTargetUrl, webhookSecretString)
		if err != nil {
			log.Error(err, "failed to setup repository for Pipelines as Code")
			r.EventRecorder.Event(&component, "Warning", "ErrorConfiguringPaCForComponentRepository", err.Error())
			return ctrl.Result{}, err
		}
		var mrMessage string
		if mrUrl != "" {
			mrMessage = fmt.Sprintf("Pipelines as Code configuration merge request: %s", mrUrl)
		} else {
			mrMessage = "Pipelines as Code configuration is up to date"
		}
		log.Info(mrMessage)
		r.EventRecorder.Event(&component, "Normal", "PipelinesAsCodeConfiguration", mrMessage)

		if mrUrl != "" {
			// PaC PR has been just created
			pipelinesAsCodeComponentProvisionTimeMetric.Observe(time.Since(component.CreationTimestamp.Time).Seconds())
		}

		// Set initial build annotation to prevent recreation of the PaC integration PR
		if err := r.Client.Get(ctx, req.NamespacedName, &component); err != nil {
			log.Error(err, "failed to get Component")
			return ctrl.Result{}, err
		}
		if len(component.Annotations) == 0 {
			component.Annotations = make(map[string]string)
		}
		component.Annotations[InitialBuildAnnotationName] = "true"
		if err := r.Client.Update(ctx, &component); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		// Use trigger build
		// Set initial build annotation to prevent next builds
		component.Annotations[InitialBuildAnnotationName] = "true"
		if err := r.Client.Update(ctx, &component); err != nil {
			return ctrl.Result{}, err
		}

		if err := r.SubmitNewBuild(ctx, component); err != nil {
			// Try to revert the initial build annotation
			if err := r.Client.Get(ctx, req.NamespacedName, &component); err == nil {
				component.Annotations[InitialBuildAnnotationName] = "false"
				if err := r.Client.Update(ctx, &component); err != nil {
					log.Error(err, fmt.Sprintf("Failed to schedule initial build for component: %v", req.NamespacedName))
				}
			} else {
				log.Error(err, fmt.Sprintf("Failed to schedule initial build for component: %v", req.NamespacedName))
			}

			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ComponentBuildReconciler) getPaCRoutePublicUrl(ctx context.Context) (string, error) {
	pacWebhookRoute := &routev1.Route{}
	pacWebhookRouteKey := types.NamespacedName{Namespace: pipelinesAsCodeNamespace, Name: pipelinesAsCodeRouteName}
	if err := r.Client.Get(ctx, pacWebhookRouteKey, pacWebhookRoute); err != nil {
		r.Log.Error(err, "failed to get Pipelines as Code route")
		return "", err
	}
	return "https://" + pacWebhookRoute.Spec.Host, nil
}

// validatePaCConfiguration detects checks that all required fields is set for whatever method is used.
func validatePaCConfiguration(gitProvider string, config map[string][]byte) error {
	isApp := gitops.IsPaCApplicationConfigured(gitProvider, config)

	expectedPaCWebhookConfigFields := []string{gitops.GetProviderTokenKey(gitProvider)}

	var err error
	switch gitProvider {
	case "github":
		if isApp {
			// GitHub application

			err = checkMandatoryFieldsNotEmpty(config, []string{gitops.PipelinesAsCode_githubAppIdKey, gitops.PipelinesAsCode_githubPrivateKey})
			if err != nil {
				break
			}

			// validate content of the fields
			if _, e := strconv.ParseInt(string(config[gitops.PipelinesAsCode_githubAppIdKey]), 10, 64); e != nil {
				err = fmt.Errorf(" Pipelines as Code: failed to parse GitHub application ID. Cause: %s", e.Error())
				break
			}

			privateKey := strings.TrimSpace(string(config[gitops.PipelinesAsCode_githubPrivateKey]))
			if !strings.HasPrefix(privateKey, "-----BEGIN RSA PRIVATE KEY-----") ||
				!strings.HasSuffix(privateKey, "-----END RSA PRIVATE KEY-----") {
				err = fmt.Errorf(" Pipelines as Code secret: GitHub application private key is invalid")
				break
			}
		} else {
			// webhook
			err = checkMandatoryFieldsNotEmpty(config, expectedPaCWebhookConfigFields)
		}

	case "gitlab":
		err = checkMandatoryFieldsNotEmpty(config, expectedPaCWebhookConfigFields)

	case "bitbucket":
		err = checkMandatoryFieldsNotEmpty(config, []string{gitops.GetProviderTokenKey(gitProvider)})
		if err != nil {
			break
		}

		if len(config["username"]) == 0 {
			err = fmt.Errorf(" Pipelines as Code secret: name of the user field must be configured")
		}

	default:
		err = fmt.Errorf("unsupported git provider: %s", gitProvider)
	}

	return err
}

func checkMandatoryFieldsNotEmpty(config map[string][]byte, mandatoryFields []string) error {
	for _, field := range mandatoryFields {
		if len(config[field]) == 0 {
			return fmt.Errorf(" Pipelines as Code secret: %s field is not configured", field)
		}
	}
	return nil
}

// Returns webhook secret for given component.
// Generates the webhook secret and saves it the k8s secret if doesn't exist.
func (r *ComponentBuildReconciler) ensureWebhookSecret(ctx context.Context, component appstudiov1alpha1.Component) (string, error) {
	webhookSecretsSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: gitops.PipelinesAsCodeWebhooksSecretName, Namespace: component.GetNamespace()}, webhookSecretsSecret); err != nil {
		if errors.IsNotFound(err) {
			webhookSecretsSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      gitops.PipelinesAsCodeWebhooksSecretName,
					Namespace: component.GetNamespace(),
					Labels: map[string]string{
						PartOfLabelName: PartOfAppStudioLabelValue,
					},
				},
			}
			if err := r.Client.Create(ctx, webhookSecretsSecret); err != nil {
				r.Log.Error(err, "failed to create webhooks secrets secret")
				return "", err
			}
			return r.ensureWebhookSecret(ctx, component)
		}

		r.Log.Error(err, "failed to get webhook secrets secret")
		return "", err
	}

	componentWebhookSecretKey := gitops.GetWebhookSecretKeyForComponent(component)
	if _, exists := webhookSecretsSecret.Data[componentWebhookSecretKey]; exists {
		// The webhook secret already exists. Use single secret for the same repository.
		return string(webhookSecretsSecret.Data[componentWebhookSecretKey]), nil
	}

	webhookSecretString := generatePaCWebhookSecretString()

	if webhookSecretsSecret.Data == nil {
		webhookSecretsSecret.Data = make(map[string][]byte)
	}
	webhookSecretsSecret.Data[componentWebhookSecretKey] = []byte(webhookSecretString)
	if err := r.Client.Update(ctx, webhookSecretsSecret); err != nil {
		r.Log.Error(err, "failed to update webhook secrets secret")
		return "", err
	}

	return webhookSecretString, nil
}

// generatePaCWebhookSecretString generates string alike openssl rand -hex 20
func generatePaCWebhookSecretString() string {
	length := 20 // in bytes
	tokenBytes := make([]byte, length)
	if _, err := rand.Read(tokenBytes); err != nil {
		panic("Failed to read from random generator")
	}
	return hex.EncodeToString(tokenBytes)
}

func (r *ComponentBuildReconciler) EnsurePaCRepository(ctx context.Context, component appstudiov1alpha1.Component, config map[string][]byte) error {
	repository, err := gitops.GeneratePACRepository(component, config)
	if err != nil {
		return err
	}

	existingRepository := &pacv1alpha1.Repository{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: repository.Name, Namespace: repository.Namespace}, existingRepository); err != nil {
		if errors.IsNotFound(err) {
			if err := controllerutil.SetOwnerReference(&component, repository, r.Scheme); err != nil {
				return err
			}
			if err := r.Client.Create(ctx, repository); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return nil
}

// ConfigureRepositoryForPaC creates a merge request with initial Pipelines as Code configuration
// and configures a webhook to notify in-cluster PaC unless application (on the repository side) is used.
func (r *ComponentBuildReconciler) ConfigureRepositoryForPaC(ctx context.Context, component *appstudiov1alpha1.Component, config map[string][]byte, webhookTargetUrl, webhookSecret string) (prUrl string, err error) {
	log := r.Log.WithValues("Repository", component.Spec.Source.GitSource.URL)

	pipelineRef, additionalPipelineParams, err := r.GetPipelineForComponent(ctx, component)
	if err != nil {
		return "", err
	}
	log.Info(fmt.Sprintf("Selected %s pipeline from %s bundle for %s component", pipelineRef.Name, pipelineRef.Bundle, component.Name))

	// Get pipeline from the bundle to be expanded to the PipelineRun
	pipelineSpec, err := retrievePipelineSpec(pipelineRef.Bundle, pipelineRef.Name)
	if err != nil {
		r.EventRecorder.Event(component, "Warning", "ErrorGettingPipelineFromBundle", err.Error())
		return "", err
	}

	pipelineOnPush, err := generatePaCPipelineRunForComponent(component, pipelineSpec, additionalPipelineParams, false)
	if err != nil {
		return "", err
	}
	pipelineOnPushYaml, err := yaml.Marshal(pipelineOnPush)
	if err != nil {
		return "", err
	}

	pipelineOnPR, err := generatePaCPipelineRunForComponent(component, pipelineSpec, additionalPipelineParams, true)
	if err != nil {
		return "", err
	}
	pipelineOnPRYaml, err := yaml.Marshal(pipelineOnPR)
	if err != nil {
		return "", err
	}

	gitProvider, _ := gitops.GetGitProvider(*component)
	isAppUsed := gitops.IsPaCApplicationConfigured(gitProvider, config)

	var accessToken string
	if !isAppUsed {
		accessToken = strings.TrimSpace(string(config[gitops.GetProviderTokenKey(gitProvider)]))
	}

	// https://github.com/owner/repository
	gitSourceUrlParts := strings.Split(strings.TrimSuffix(component.Spec.Source.GitSource.URL, ".git"), "/")

	commitMessage := "Appstudio update " + component.Name
	branch := "appstudio-" + component.Name
	baseBranch := "main"
	mrTitle := "Appstudio update " + component.Name
	mrText := "Pipelines as Code configuration proposal"
	authorName := "redhat-appstudio"
	authorEmail := "appstudio@redhat.com"

	switch gitProvider {
	case "github":
		owner := gitSourceUrlParts[3]
		repository := gitSourceUrlParts[4]

		var ghclient *github.GithubClient
		if isAppUsed {
			githubAppIdStr := string(config[gitops.PipelinesAsCode_githubAppIdKey])
			githubAppId, err := strconv.ParseInt(githubAppIdStr, 10, 64)
			if err != nil {
				return "", fmt.Errorf("failed to convert %s to int: %w", githubAppIdStr, err)
			}

			privateKey := config[gitops.PipelinesAsCode_githubPrivateKey]
			ghclient, err = github.NewGithubClientByApp(githubAppId, privateKey, owner)
			if err != nil {
				return "", err
			}
		} else {
			// Webhook
			ghclient = github.NewGithubClient(accessToken)

			err = github.SetupPaCWebhook(ghclient, webhookTargetUrl, webhookSecret, owner, repository)
			if err != nil {
				return "", fmt.Errorf("failed to configure Pipelines as Code webhook: %w", err)
			} else {
				fmt.Printf("Pipelines as Code webhook \"%s\" configured for %s component in %s namespace\n", webhookTargetUrl, component.GetName(), component.GetNamespace())
			}
		}

		prData := &github.PaCPullRequestData{
			Owner:         owner,
			Repository:    repository,
			CommitMessage: commitMessage,
			Branch:        branch,
			BaseBranch:    baseBranch,
			PRTitle:       mrTitle,
			PRText:        mrText,
			AuthorName:    authorName,
			AuthorEmail:   authorEmail,
			Files: []github.File{
				{FullPath: ".tekton/" + component.Name + "-" + PipelineRunOnPushFilename, Content: pipelineOnPushYaml},
				{FullPath: ".tekton/" + component.Name + "-" + PipelineRunOnPRFilename, Content: pipelineOnPRYaml},
			},
		}
		prUrl, err = github.CreatePaCPullRequest(ghclient, prData)
		if err != nil {
			// Handle case when GitHub application is not installed for the component repository
			if strings.Contains(err.Error(), "Resource not accessible by integration") {
				return "", fmt.Errorf(" Pipelines as Code GitHub application with %s ID is not installed for %s repository",
					string(config[gitops.PipelinesAsCode_githubAppIdKey]), component.Spec.Source.GitSource.URL)
			}
			return "", err
		}

		return prUrl, nil

	case "gitlab":
		glclient, err := gitlab.NewGitlabClient(accessToken)
		if err != nil {
			return "", err
		}

		gitlabNamespace := gitSourceUrlParts[3]
		gitlabProjectName := gitSourceUrlParts[4]
		projectPath := gitlabNamespace + "/" + gitlabProjectName

		err = gitlab.SetupPaCWebhook(glclient, projectPath, webhookTargetUrl, webhookSecret)
		if err != nil {
			return "", err
		}

		mrData := &gitlab.PaCMergeRequestData{
			ProjectPath:   projectPath,
			CommitMessage: commitMessage,
			Branch:        branch,
			BaseBranch:    baseBranch,
			MrTitle:       mrTitle,
			MrText:        mrText,
			AuthorName:    authorName,
			AuthorEmail:   authorEmail,
			Files: []gitlab.File{
				{FullPath: ".tekton/" + component.Name + "-" + PipelineRunOnPushFilename, Content: pipelineOnPushYaml},
				{FullPath: ".tekton/" + component.Name + "-" + PipelineRunOnPRFilename, Content: pipelineOnPRYaml},
			},
		}
		mrUrl, err := gitlab.EnsurePaCMergeRequest(glclient, mrData)
		return mrUrl, err

	case "bitbucket":
		// TODO implement
		return "", fmt.Errorf("git provider %s is not supported", gitProvider)
	default:
		return "", fmt.Errorf("git provider %s is not supported", gitProvider)
	}
}

// generatePaCPipelineRunForComponent returns pipeline run definition to build component source with.
// Generated pipeline run contains placeholders that are expanded by Pipeline-as-Code.
func generatePaCPipelineRunForComponent(component *appstudiov1alpha1.Component, pipelineSpec *tektonapi.PipelineSpec, additionalPipelineParams []tektonapi.Param, onPull bool) (*tektonapi.PipelineRun, error) {
	var targetBranches []string
	if component.Spec.Source.GitSource != nil && component.Spec.Source.GitSource.Revision != "" {
		targetBranches = []string{component.Spec.Source.GitSource.Revision}
	} else {
		targetBranches = []string{"main", "master"}
	}

	annotations := map[string]string{
		"pipelinesascode.tekton.dev/on-target-branch": "[" + strings.Join(targetBranches[:], ",") + "]",
		"pipelinesascode.tekton.dev/max-keep-runs":    "3",
		"build.appstudio.redhat.com/commit_sha":       "{{revision}}",
		"build.appstudio.redhat.com/target_branch":    "{{target_branch}}",
	}
	labels := map[string]string{
		ApplicationNameLabelName:                component.Spec.Application,
		ComponentNameLabelName:                  component.Name,
		"pipelines.appstudio.openshift.io/type": "build",
	}

	imageRepo := strings.Split(component.Spec.ContainerImage, ":")[0]
	var pipelineName string
	var proposedImage string
	if onPull {
		annotations["pipelinesascode.tekton.dev/on-event"] = "[pull_request]"
		annotations["build.appstudio.redhat.com/pull_request_number"] = "{{pull_request_number}}"
		pipelineName = component.Name + PipelineRunOnPRSuffix
		proposedImage = imageRepo + ":on-pr-{{revision}}"
	} else {
		annotations["pipelinesascode.tekton.dev/on-event"] = "[push]"
		pipelineName = component.Name + PipelineRunOnPushSuffix
		proposedImage = imageRepo + ":{{revision}}"
	}

	params := []tektonapi.Param{
		{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "{{repo_url}}"}},
		{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "{{revision}}"}},
		{Name: "output-image", Value: tektonapi.ArrayOrString{Type: "string", StringVal: proposedImage}},
	}

	dockerFile, err := devfile.SearchForDockerfile([]byte(component.Status.Devfile))
	if err != nil {
		return nil, err
	}
	if dockerFile != nil {
		if dockerFile.Uri != "" {
			params = append(params, tektonapi.Param{Name: "dockerfile", Value: tektonapi.ArrayOrString{Type: "string", StringVal: dockerFile.Uri}})
		}
		if dockerFile.BuildContext != "" {
			params = append(params, tektonapi.Param{Name: "path-context", Value: tektonapi.ArrayOrString{Type: "string", StringVal: dockerFile.BuildContext}})
		}
	}

	params = mergeAndSortTektonParams(params, additionalPipelineParams)

	pipelineRun := &tektonapi.PipelineRun{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PipelineRun",
			APIVersion: "tekton.dev/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        pipelineName,
			Namespace:   component.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: tektonapi.PipelineRunSpec{
			PipelineSpec: pipelineSpec,
			Params:       params,
			Workspaces: []tektonapi.WorkspaceBinding{
				{
					Name:                "workspace",
					VolumeClaimTemplate: gitops.GenerateVolumeClaimTemplate(),
				},
				{
					Name:   "registry-auth",
					Secret: &corev1.SecretVolumeSource{SecretName: gitopsprepare.RegistrySecret},
				},
			},
		},
	}

	return pipelineRun, nil
}

// mergeAndSortTektonParams merges additional params into existing params by adding new or replacing existing values.
func mergeAndSortTektonParams(existedParams, additionalParams []tektonapi.Param) []tektonapi.Param {
	var params []tektonapi.Param
	paramsMap := make(map[string]tektonapi.Param)
	for _, p := range existedParams {
		paramsMap[p.Name] = p
	}
	for _, p := range additionalParams {
		paramsMap[p.Name] = p
	}
	for _, v := range paramsMap {
		params = append(params, v)
	}
	sort.Slice(params, func(i, j int) bool {
		return params[i].Name < params[j].Name
	})
	return params
}

// GetPipelineForComponent searches for the build pipeline to use on the component.
func (r *ComponentBuildReconciler) GetPipelineForComponent(ctx context.Context, component *appstudiov1alpha1.Component) (*tektonapi.PipelineRef, []tektonapi.Param, error) {
	var pipelineSelectors []buildappstudiov1alpha1.BuildPipelineSelector
	pipelineSelector := &buildappstudiov1alpha1.BuildPipelineSelector{}

	pipelineSelectorKeys := []types.NamespacedName{
		// First try specific config for the application
		{Namespace: component.Namespace, Name: component.Spec.Application},
		// Second try namespaced config
		{Namespace: component.Namespace, Name: buildPipelineSelectorResourceName},
		// Finally try global config
		{Namespace: buildServiceNamespaceName, Name: buildPipelineSelectorResourceName},
	}

	for _, pipelineSelectorKey := range pipelineSelectorKeys {
		if err := r.Client.Get(ctx, pipelineSelectorKey, pipelineSelector); err != nil {
			if !errors.IsNotFound(err) {
				return nil, nil, err
			}
			// The config is not found, try the next one in the hierarchy
		} else {
			pipelineSelectors = append(pipelineSelectors, *pipelineSelector)
		}
	}

	if len(pipelineSelectors) > 0 {
		pipelineRef, pipelineParams, err := pipelineselector.SelectPipelineForComponent(component, pipelineSelectors)
		if err != nil {
			return nil, nil, err
		}
		if pipelineRef != nil {
			return pipelineRef, pipelineParams, nil
		}
	}

	// Fallback to the default pipeline
	return &tektonapi.PipelineRef{
		Name:   defaultPipelineName,
		Bundle: gitopsprepare.AppStudioFallbackBuildBundle,
	}, nil, nil
}

// retrievePipelineSpec retrieves pipeline definition with given name from the given bundle.
func retrievePipelineSpec(bundleUri, pipelineName string) (*tektonapi.PipelineSpec, error) {
	var obj runtime.Object
	var err error
	resolver := oci.NewResolver(bundleUri, authn.DefaultKeychain)

	if obj, err = resolver.Get(context.TODO(), "pipeline", pipelineName); err != nil {
		return nil, err
	}
	pipelineSpecObj, ok := obj.(tektonapi.PipelineObject)
	if !ok {
		return nil, fmt.Errorf("failed to extract pipeline %s from bundle %s", bundleUri, pipelineName)
	}
	pipelineSpec := pipelineSpecObj.PipelineSpec()
	return &pipelineSpec, nil
}

// SubmitNewBuild creates a new PipelineRun to build a new image for the given component.
// Is called only once on component creation if Pipelines as Code is not configured,
// otherwise the build is handled by PaC.
func (r *ComponentBuildReconciler) SubmitNewBuild(ctx context.Context, component appstudiov1alpha1.Component) error {
	log := r.Log.WithValues("Namespace", component.Namespace, "Application", component.Spec.Application, "Component", component.Name)

	gitSecretName := component.Spec.Secret
	// Make the Secret ready for consumption by Tekton.
	if gitSecretName != "" {
		gitSecret := corev1.Secret{}
		err := r.Client.Get(ctx, types.NamespacedName{Name: gitSecretName, Namespace: component.Namespace}, &gitSecret)
		if err != nil {
			log.Error(err, fmt.Sprintf("Secret %s is missing", gitSecretName))
			return err
		} else {
			if gitSecret.Annotations == nil {
				gitSecret.Annotations = map[string]string{}
			}

			gitHost, _ := getGitProviderUrl(component.Spec.Source.GitSource.URL)

			// Doesn't matter if it was present, we will always override.
			gitSecret.Annotations["tekton.dev/git-0"] = gitHost
			err = r.Client.Update(ctx, &gitSecret)
			if err != nil {
				log.Error(err, fmt.Sprintf("Secret %s update failed", gitSecretName))
				return err
			}
		}
	}

	pipelinesServiceAccount := corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: "pipeline", Namespace: component.Namespace}, &pipelinesServiceAccount)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("Failed to read service account %s in namespace %s", buildPipelineServiceAccountName, component.Namespace))
			return err
		}
		// Create service account for the build pipeline
		buildPipelineSA := corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      buildPipelineServiceAccountName,
				Namespace: component.Namespace,
			},
		}
		if err := r.Client.Create(ctx, &buildPipelineSA); err != nil {
			log.Error(err, fmt.Sprintf("Failed to create service account %s in namespace %s", buildPipelineServiceAccountName, component.Namespace))
			return err
		}
		return r.SubmitNewBuild(ctx, component)
	} else {
		updateRequired := updateServiceAccountIfSecretNotLinked(gitSecretName, &pipelinesServiceAccount)
		if updateRequired {
			err = r.Client.Update(ctx, &pipelinesServiceAccount)
			if err != nil {
				log.Error(err, fmt.Sprintf("Unable to update pipeline service account %v", pipelinesServiceAccount))
				return err
			}
			log.Info(fmt.Sprintf("Service Account updated %v", pipelinesServiceAccount))
		}
	}

	// Create initial build pipeline

	pipelineRef, additionalPipelineParams, err := r.GetPipelineForComponent(ctx, &component)
	if err != nil {
		return err
	}

	initialBuildPipelineRun, err := generateInitialPipelineRunForComponent(&component, pipelineRef, additionalPipelineParams)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to generate PipelineRun to build %s component in %s namespace", component.Name, component.Namespace))
		return err
	}

	err = controllerutil.SetOwnerReference(&component, initialBuildPipelineRun, r.Scheme)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to set owner reference for %v", initialBuildPipelineRun))
	}

	err = r.Client.Create(ctx, initialBuildPipelineRun)
	if err != nil {
		log.Error(err, fmt.Sprintf("Unable to create the build PipelineRun %v", initialBuildPipelineRun))
		return err
	}

	initialBuildPipelineCreationTimeMetric.Observe(time.Since(component.CreationTimestamp.Time).Seconds())

	log.Info(fmt.Sprintf("Initial build pipeline %s created for component %s in %s namespace using %s pipeline from %s bundle",
		initialBuildPipelineRun.Name, component.Name, component.Namespace, pipelineRef.Name, pipelineRef.Bundle))

	return nil
}

func generateInitialPipelineRunForComponent(component *appstudiov1alpha1.Component, pipelineRef *tektonapi.PipelineRef, additionalPipelineParams []tektonapi.Param) (*tektonapi.PipelineRun, error) {
	timestamp := time.Now().Unix()
	pipelineGenerateName := fmt.Sprintf("%s-", component.Name)
	revision := "main"
	if component.Spec.Source.GitSource != nil && component.Spec.Source.GitSource.Revision != "" {
		revision = component.Spec.Source.GitSource.Revision
	}
	image := fmt.Sprintf("%s:initial-build-%d", strings.Split(component.Spec.ContainerImage, ":")[0], timestamp)

	params := []tektonapi.Param{
		{Name: "git-url", Value: tektonapi.ArrayOrString{Type: "string", StringVal: component.Spec.Source.GitSource.URL}},
		{Name: "revision", Value: tektonapi.ArrayOrString{Type: "string", StringVal: revision}},
		{Name: "output-image", Value: tektonapi.ArrayOrString{Type: "string", StringVal: image}},
	}
	if value, exists := component.Annotations["skip-initial-checks"]; exists && (value == "1" || strings.ToLower(value) == "true") {
		params = append(params, tektonapi.Param{Name: "skip-checks", Value: tektonapi.ArrayOrString{Type: "string", StringVal: "true"}})
	}

	dockerFile, err := devfile.SearchForDockerfile([]byte(component.Status.Devfile))
	if err != nil {
		return nil, err
	}
	if dockerFile != nil {
		if dockerFile.Uri != "" {
			params = append(params, tektonapi.Param{Name: "dockerfile", Value: tektonapi.ArrayOrString{Type: "string", StringVal: dockerFile.Uri}})
		}
		if dockerFile.BuildContext != "" {
			params = append(params, tektonapi.Param{Name: "path-context", Value: tektonapi.ArrayOrString{Type: "string", StringVal: dockerFile.BuildContext}})
		}
	}

	params = mergeAndSortTektonParams(params, additionalPipelineParams)

	pipelineRun := &tektonapi.PipelineRun{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PipelineRun",
			APIVersion: "tekton.dev/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: pipelineGenerateName,
			Namespace:    component.Namespace,
			Labels: map[string]string{
				ApplicationNameLabelName:                component.Spec.Application,
				ComponentNameLabelName:                  component.Name,
				"pipelines.appstudio.openshift.io/type": "build",
			},
			Annotations: map[string]string{
				"build.appstudio.redhat.com/target_branch": revision,
				"build.appstudio.redhat.com/pipeline_name": pipelineRef.Name,
				"build.appstudio.redhat.com/bundle":        pipelineRef.Bundle,
			},
		},
		Spec: tektonapi.PipelineRunSpec{
			PipelineRef: pipelineRef,
			Params:      params,
			Workspaces: []tektonapi.WorkspaceBinding{
				{
					Name:                "workspace",
					VolumeClaimTemplate: gitops.GenerateVolumeClaimTemplate(),
				},
				{
					Name:   "registry-auth",
					Secret: &corev1.SecretVolumeSource{SecretName: gitopsprepare.RegistrySecret},
				},
			},
		},
	}

	return pipelineRun, nil
}

// getGitProviderUrl takes a Git URL and returns git provider host.
// Examples:
//
//	For https://github.com/foo/bar returns https://github.com
//	For git@github.com:foo/bar returns https://github.com
func getGitProviderUrl(gitURL string) (string, error) {
	if strings.HasPrefix(gitURL, "git@") {
		host := strings.Split(strings.TrimPrefix(gitURL, "git@"), ":")[0]
		return "https://" + host, nil
	}

	u, err := url.Parse(gitURL)

	// We really need the format of the string to be correct.
	// We'll not do any autocorrection.
	if err != nil || u.Scheme == "" {
		return "", fmt.Errorf("failed to parse string into a URL: %v or scheme is empty", err)
	}
	return u.Scheme + "://" + u.Host, nil
}

func updateServiceAccountIfSecretNotLinked(gitSecretName string, serviceAccount *corev1.ServiceAccount) bool {
	for _, credentialSecret := range serviceAccount.Secrets {
		if credentialSecret.Name == gitSecretName {
			// The secret is present in the service account, no updates needed
			return false
		}
	}

	// Add the secret to secret account and return that update is needed
	serviceAccount.Secrets = append(serviceAccount.Secrets, corev1.ObjectReference{Name: gitSecretName})
	return true
}
