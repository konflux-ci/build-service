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
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/redhat-appstudio/application-service/gitops"
	gitopsprepare "github.com/redhat-appstudio/application-service/gitops/prepare"
	buildappstudiov1alpha1 "github.com/redhat-appstudio/build-service/api/v1alpha1"
	"github.com/redhat-appstudio/build-service/pkg/github"
	batch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	RenovateConfigName = "renovate-config"
	RenovateImageUrl   = "quay.io/redhat-appstudio/renovate:34.154-slim"
)

// GitTektonResourcesRenovater watches AppStudio BuildPipelineSelector object in order to update
// existing .tekton directories.
type GitTektonResourcesRenovater struct {
	Client        client.Client
	Scheme        *runtime.Scheme
	Log           logr.Logger
	EventRecorder record.EventRecorder
}

// SetupWithManager sets up the controller with the Manager.
func (r *GitTektonResourcesRenovater) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&buildappstudiov1alpha1.BuildPipelineSelector{}).Complete(r)
}

// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;delete;deletecollection

func (r *GitTektonResourcesRenovater) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	// Only process main buildPipelineSelector
	if req.Namespace != buildServiceNamespaceName && req.Name != buildPipelineSelectorResourceName {
		return ctrl.Result{}, nil
	}

	// Check if GitHub Application is used, if not then skip
	pacSecret := corev1.Secret{}
	globalPaCSecretKey := types.NamespacedName{Namespace: buildServiceNamespaceName, Name: gitopsprepare.PipelinesAsCodeSecretName}
	if err := r.Client.Get(ctx, globalPaCSecretKey, &pacSecret); err != nil {
		if !errors.IsNotFound(err) {
			r.EventRecorder.Event(&pacSecret, "Warning", "ErrorReadingPaCSecret", err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to get Pipelines as Code secret in %s namespace: %w", globalPaCSecretKey.Namespace, err)
		}
	}
	isApp := gitops.IsPaCApplicationConfigured("github", pacSecret.Data)
	if !isApp {
		r.Log.Info("GitHub App is not set")
		return ctrl.Result{}, nil
	}

	// Load GitHub App and get GitHub Installations
	githubAppIdStr := string(pacSecret.Data[gitops.PipelinesAsCode_githubAppIdKey])
	githubAppId, err := strconv.ParseInt(githubAppIdStr, 10, 64)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to convert %s to int: %w", githubAppIdStr, err)
	}
	privateKey := pacSecret.Data[gitops.PipelinesAsCode_githubPrivateKey]
	installations, slug, err := github.GetInstallations(githubAppId, privateKey)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Update config.js file for Jobs
	configmap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: RenovateConfigName, Namespace: buildServiceNamespaceName},
		Data: map[string]string{
			"config.js": generateConfigJS(slug),
		},
	}
	if err := r.Client.Delete(ctx, &configmap); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}
	if err := r.Client.Create(ctx, &configmap); err != nil {
		r.Log.Error(err, "failed to create configmap")
		return ctrl.Result{}, err
	}
	for _, installation := range installations {
		err = r.CreateRenovaterJob(ctx, installation)
		if err != nil {
			r.Log.Error(err, "failed to create a job")
		}
		time.Sleep(10 * time.Second)
	}

	return ctrl.Result{RequeueAfter: 10 * time.Hour}, nil
}

func generateConfigJS(slug string) string {
	template := `
	module.exports = {
		username: "%s[bot]",
		gitAuthor:"AppStudio <123456+%s[bot]@users.noreply.github.com>",
		onboarding: false,
		requireConfig: "ignored",
		autodiscover: true,
		enabledManagers: ["tekton"],
		tekton: {
			fileMatch: ["\\.yaml$", "\\.yml$"],
			includePaths: [".tekton/**"],
			packageRules: [
			  {
				matchPackagePatterns: ["*"],
				groupName: "tekton references"
			  }
			]
		},
		includeForks: true,
		dependencyDashboard: false
	}
	`
	return fmt.Sprintf(template, slug, slug)
}

func (r *GitTektonResourcesRenovater) CreateRenovaterJob(ctx context.Context, installation github.ApplicationInstallation) error {
	name := fmt.Sprintf("renovate-job-%d", installation.InstallationID)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: buildServiceNamespaceName,
		},
		StringData: map[string]string{
			"token": installation.Token,
		},
	}
	trueBool := true
	falseBool := false
	backoffLimit := int32(1)
	job := &batch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: buildServiceNamespaceName,
		},
		Spec: batch.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: RenovateConfigName,
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: RenovateConfigName},
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "renovate",
							Image: RenovateImageUrl,
							Env: []corev1.EnvVar{
								{
									Name: "RENOVATE_TOKEN",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: name,
											},
											Key: "token",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      RenovateConfigName,
									MountPath: "/usr/src/app/config.js",
									SubPath:   "config.js",
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								RunAsNonRoot:             &trueBool,
								AllowPrivilegeEscalation: &falseBool,
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
	if err := r.Client.Delete(ctx, secret); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
	}
	if err := r.Client.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
	}
	if err := r.Client.Create(ctx, secret); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, job); err != nil {
		return err
	}
	r.Log.Info(fmt.Sprintf("Job %s triggered", job.Name))
	return nil
}
