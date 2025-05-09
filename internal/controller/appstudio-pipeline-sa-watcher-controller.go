/*
Copyright 2025 Red Hat, Inc.

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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	appstudiov1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	l "github.com/konflux-ci/build-service/pkg/logs"
	imgcv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
)

// TODO delete the controller after migration to the new dedicated to build Service Account.
// AppstudioPipelineServiceAccountWatcherReconciler watches appstudio-pippeline Service Account
// and syncs linked secrets updates to dedicated for build pipeline Service Account.
type AppstudioPipelineServiceAccountWatcherReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppstudioPipelineServiceAccountWatcherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ServiceAccount{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return false
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// React only if appstudio-pipeline Service Account is changed
				sa, ok := e.ObjectNew.(*corev1.ServiceAccount)
				if !ok {
					return false
				}
				return sa.Name == buildPipelineServiceAccountName
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		})).
		Named("AppstudioPipelineServiceAccountWatcher").
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories/status,verbs=get;list;watch

func (r *AppstudioPipelineServiceAccountWatcherReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("appstudioPipelineSA")
	log.Info("Synching secrets")

	componentList := &appstudiov1alpha1.ComponentList{}
	if err := r.Client.List(ctx, componentList, client.InNamespace(req.Namespace)); err != nil {
		log.Info("failed to list Components")
		return ctrl.Result{}, err
	}

	commonServiceAccount := &corev1.ServiceAccount{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: req.Namespace}, commonServiceAccount); err != nil {
		if errors.IsNotFound(err) {
			// Assume the migration to the dedicated build Service Account has been done, nothing to do.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	imageRepositoryList := &imgcv1alpha1.ImageRepositoryList{}
	if err := r.Client.List(ctx, imageRepositoryList, client.InNamespace(req.Namespace)); err != nil {
		log.Error(err, "failed to list ImageRepositories in namespace", l.Action, l.ActionView)
		return ctrl.Result{}, err
	}
	// Wait all Image Repositories are provisioned
	for _, imageRepository := range imageRepositoryList.Items {
		if imageRepository.Status.State == "" {
			// Wait Image Controller to finish
			log.Info("Waiting for Image Repository provision", "ImageRepositoryName", imageRepository.Name)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	}

	for _, component := range componentList.Items {
		dedicatedBuildPipelineServiceAccountName := getBuildPipelineServiceAccountName(&component)
		buildPipelinesServiceAccount := &corev1.ServiceAccount{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: dedicatedBuildPipelineServiceAccountName, Namespace: req.Namespace}, buildPipelinesServiceAccount); err != nil {
			if !errors.IsNotFound(err) {
				log.Error(err, fmt.Sprintf("failed to read build pipeline Service Account %s in namespace %s", dedicatedBuildPipelineServiceAccountName, component.Namespace), l.Action, l.ActionView)
				// do not break sync because of a faulty item
			}
			// Dedicated build pipeline Service Account hasn't been yet created.
			// Skip for now, the sync will be performed on the Service Account creation.
			continue
		}

		if err := LinkCommonAppstudioPipelineSecretsToNewServiceAccount(ctx, r.Client, &component, commonServiceAccount, imageRepositoryList.Items); err != nil {
			log.Error(err, "failed to sync linked secrets for Component", "Component", component.Name, l.Action, l.ActionUpdate)
			// do not break sync because of a faulty item
		}
	}

	return ctrl.Result{}, nil
}
