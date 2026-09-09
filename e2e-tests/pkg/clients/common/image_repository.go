package common

import (
	"context"
	"fmt"
	"time"

	"github.com/konflux-ci/build-service/e2e-tests/pkg/utils"
	"github.com/konflux-ci/image-controller/api/konflux/v1alpha1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	componentNameLabelName               = "build.konflux-ci.dev/component"
	updateComponentAnnotationName        = "build.konflux-ci.dev/update-component-image"
	skipRepositoryDeletionAnnotationName = "build.konflux-ci.dev/skip-repository-deletion"
)

// CreateImageRepositoryCR creates new ImageRepository
func (s *SuiteController) CreateImageRepositoryCR(imageRepoCRName, namespace, visibility, userDefinedImageName, componentName string, addUpdateAnnotation bool, skipRepositoryDeletion bool) (*v1alpha1.ImageRepository, error) {

	var imageParams v1alpha1.ImageParameters
	if visibility != "" {
		imageParams.Visibility = v1alpha1.ImageVisibility(visibility)
	}
	if userDefinedImageName != "" {
		imageParams.Name = userDefinedImageName
	}

	var labels map[string]string
	if componentName != "" {
		labels = map[string]string{componentNameLabelName: componentName}
	}

	annotations := map[string]string{}
	if addUpdateAnnotation {
		annotations[updateComponentAnnotationName] = "true"
	}
	if skipRepositoryDeletion {
		annotations[skipRepositoryDeletionAnnotationName] = "true"
	}

	imageRepository := &v1alpha1.ImageRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:        imageRepoCRName,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: v1alpha1.ImageRepositorySpec{
			Image: imageParams,
		},
	}

	err := s.KubeRest().Create(context.Background(), imageRepository)
	if err != nil {
		return nil, err
	}
	return imageRepository, nil
}

// GetImageRepository returns the image repository CR
func (s *SuiteController) GetImageRepository(imageRepoName, namespace string) (*v1alpha1.ImageRepository, error) {
	namespacedName := types.NamespacedName{
		Name:      imageRepoName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}

	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return nil, err
	}
	return &imageRepository, err
}

// WaitForImageRepositoryToBeReady waits for the image repository status to be in ready state
func (s *SuiteController) WaitForImageRepositoryToBeReady(name, namespace string) error {
	namespacedName := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}

	err := utils.WaitUntil(func() (done bool, err error) {
		if err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository); err != nil {
			fmt.Printf("Image repository %q do not have right state ('%s' != 'ready') yet but it has status %v.\n", name, imageRepository.Status.State, imageRepository.Status)
			return false, nil
		}
		return imageRepository.Status.State == "ready", nil
	}, 2*time.Minute)

	return err
}

// GetImageNameFromImageRepositoryCR returns the image repo name from the image repository CR
func (s *SuiteController) GetImageNameFromImageRepositoryCR(namespace, imageRepoCRName string) (string, error) {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}

	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return "", err
	}
	return imageRepository.Spec.Image.Name, err
}

// GetImageURLFromIR reads and returns the image url from the image repository
func (s *SuiteController) GetImageURLFromIR(imageRepoName, namespace string) (string, error) {
	namespacedName := types.NamespacedName{
		Name:      imageRepoName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return "", err
	}
	return imageRepository.Status.Image.URL, err
}

// GetGenerateTimestamp return the generationTimestamp of credentials of image repository
func (s *SuiteController) GetGenerateTimestamp(imageRepoName, namespace string) (string, error) {
	namespacedName := types.NamespacedName{
		Name:      imageRepoName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return "", err
	}
	return imageRepository.Status.Credentials.GenerationTimestamp.String(), nil
}

// GetRobotAccountsFromImageRepositoryCR returns the pull and push robot accounts from the image repository CR
func (s *SuiteController) GetRobotAccountsFromImageRepositoryCR(namespace, imageRepoCRName string) (string, string, error) {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}

	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return "", "", err
	}

	return imageRepository.Status.Credentials.PullRobotAccountName, imageRepository.Status.Credentials.PushRobotAccountName, nil
}

// GetSecretsFromImageRepositoryCR returns the pull and push secrets from the image repository CR
func (s *SuiteController) GetSecretsFromImageRepositoryCR(namespace, imageRepoCRName string) (string, string, error) {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}

	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return "", "", err
	}

	return imageRepository.Status.Credentials.PullSecretName, imageRepository.Status.Credentials.PushSecretName, nil
}

// DeleteImageRepositoryCR removes the ImageRepository object
func (s *SuiteController) DeleteImageRepositoryCR(name, namespace string) error {
	imageRepository := v1alpha1.ImageRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := s.KubeRest().Delete(context.Background(), &imageRepository); err != nil {
		if !k8sErrors.IsNotFound(err) {
			return fmt.Errorf("error deleting imageRepository: %+v", err)
		}
	}
	return nil
}

func (s *SuiteController) UpdateImageName(imageRepoCRName, namespace, updatedImageName string) error {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return err
	}

	imageRepository.Spec.Image.Name = updatedImageName

	err = s.KubeRest().Update(context.Background(), &imageRepository)
	if err != nil {
		return err
	}
	return nil
}

func (s *SuiteController) UpdateVisibility(imageRepoCRName, namespace, updatedVisibility string) error {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return err
	}

	imageRepository.Spec.Image.Visibility = v1alpha1.ImageVisibility(updatedVisibility)

	err = s.KubeRest().Update(context.Background(), &imageRepository)
	if err != nil {
		return err
	}
	return nil
}

// RegenerateToken will set the spec.credentials.regenerate-token to true for credential rotation of robot accounts
func (s *SuiteController) RegenerateToken(imageRepoCRName, namespace string) error {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return err
	}

	regenerateToken := true
	credentials := &v1alpha1.ImageCredentials{
		RegenerateToken: &regenerateToken,
	}
	imageRepository.Spec.Credentials = credentials

	err = s.KubeRest().Update(context.Background(), &imageRepository)
	if err != nil {
		return err
	}
	return nil
}

// RegenerateNamespacePullToken will set the spec.credentials.regenerate-namespace-pull-token to true for rotation of namespace pull token
func (s *SuiteController) RegenerateNamespacePullToken(imageRepoCRName, namespace string) error {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return err
	}

	regenerateToken := true
	credentials := &v1alpha1.ImageCredentials{
		RegenerateNamespacePullToken: &regenerateToken,
	}
	imageRepository.Spec.Credentials = credentials

	err = s.KubeRest().Update(context.Background(), &imageRepository)
	if err != nil {
		return err
	}
	return nil
}

// SetVerifyLinking will set the spec.credentials.verify-linking to true
func (i *SuiteController) SetVerifyLinking(imageRepoName, namespace string) error {
	namespacedName := types.NamespacedName{
		Name:      imageRepoName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := i.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return err
	}

	verifyLinking := true
	credentials := &v1alpha1.ImageCredentials{
		VerifyLinking: &verifyLinking,
	}
	imageRepository.Spec.Credentials = credentials

	err = i.KubeRest().Update(context.Background(), &imageRepository)
	if err != nil {
		return err
	}
	return nil
}

func (s *SuiteController) RemoveFinalizerFromIR(imageRepoCRName, namespace string) error {
	finalizerName := "konflux-ci.dev/image-repository"
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return err
	}
	patch := client.MergeFrom(imageRepository.DeepCopy())
	if ok := controllerutil.RemoveFinalizer(&imageRepository, finalizerName); ok {
		err = s.KubeRest().Patch(context.Background(), &imageRepository, patch)
		if err != nil {
			return err
		}
		return nil
	} else {
		return fmt.Errorf("failed to remove finalizer %s from image repository", finalizerName)
	}
}

func (s *SuiteController) AddAnnotationsToIR(imageRepoCRName, namespace string, annotations map[string]string) error {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return err
	}

	for key, value := range annotations {
		imageRepository.Annotations[key] = value
	}

	err = s.KubeRest().Update(context.Background(), &imageRepository)
	if err != nil {
		return err
	}
	return nil
}

func (s *SuiteController) UpdatePushSecretName(imageRepoCRName, namespace, updatedName string) error {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return err
	}
	patch := client.MergeFrom(imageRepository.DeepCopy())
	imageRepository.Status.Credentials.PushSecretName = updatedName
	err = s.KubeRest().Status().Patch(context.Background(), &imageRepository, patch)
	if err != nil {
		return err
	}
	return nil
}

func (s *SuiteController) AddNotifictionToIR(imageRepoCRName, namespace, title string) error {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return err
	}
	webhookUrl := utils.GetEnv("SMEE_CHANNEL", "")
	newNotification := v1alpha1.Notifications{
		Title:  title,
		Event:  "repo_push",
		Method: "webhook",
		Config: v1alpha1.NotificationConfig{
			Url: webhookUrl,
		},
	}
	imageRepository.Spec.Notifications = append(imageRepository.Spec.Notifications, newNotification)
	err = s.KubeRest().Update(context.Background(), &imageRepository)
	if err != nil {
		return err
	}
	return nil
}

func (s *SuiteController) GetMatchingNotificationStatus(imageRepoCRName, namespace, notificationTitle string) (*v1alpha1.NotificationStatus, error) {
	namespacedName := types.NamespacedName{
		Name:      imageRepoCRName,
		Namespace: namespace,
	}
	imageRepository := v1alpha1.ImageRepository{}
	var notificationStatus *v1alpha1.NotificationStatus
	err := s.KubeRest().Get(context.Background(), namespacedName, &imageRepository)
	if err != nil {
		return notificationStatus, err
	}
	for index := range imageRepository.Status.Notifications {
		notStatus := &imageRepository.Status.Notifications[index]
		if notStatus.Title == notificationTitle {
			return notStatus, nil
		}
	}
	return notificationStatus, fmt.Errorf("does not find any notification matching the title")
}
