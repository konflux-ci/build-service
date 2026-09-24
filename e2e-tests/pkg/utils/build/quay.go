package build

import (
	"net/http"
	"strings"

	"github.com/konflux-ci/build-service/e2e-tests/pkg/utils"
	"github.com/konflux-ci/image-controller/pkg/quay"
)

var (
	quayApiUrl = "https://quay.io/api/v1"
	quayOrg    = utils.GetEnv("DEFAULT_QUAY_ORG", "redhat-appstudio-qe")
	quayToken  = utils.GetEnv("DEFAULT_QUAY_ORG_TOKEN", "")
	quayClient = quay.NewQuayClient(&http.Client{Transport: utils.NewRetryTransport(&http.Transport{})}, quayToken, quayApiUrl)
)

func DoesImageRepoExistInQuay(quayImageRepoName string) (bool, error) {
	exists, err := quayClient.RepositoryExists(quayOrg, quayImageRepoName)
	if exists {
		return true, nil
	} else if err != nil && strings.Contains(err.Error(), "does not exist") {
		return false, nil
	}
	return false, err
}

func DoesRobotAccountExistInQuay(robotAccountName string) (bool, error) {
	response, err := quayClient.GetRobotAccount(quayOrg, robotAccountName)
	if response == nil && err == nil {
		return false, nil
	} else if response != nil && err == nil {
		return true, nil
	} else {
		return false, err
	}
}

func DeleteImageRepo(imageName string) (bool, error) {
	if imageName == "" {
		return false, nil
	}
	_, err := quayClient.DeleteRepository(quayOrg, imageName)
	if err != nil {
		return false, err
	}
	return true, nil
}

func IsImageRepoPublic(quayImageRepoName string) (bool, error) {
	return quayClient.IsRepositoryPublic(quayOrg, quayImageRepoName)
}

func DoesNotificationExists(quayImageRepoName, notificationTile string) (bool, error) {
	quayNotifications, err := quayClient.GetNotifications(quayOrg, quayImageRepoName)
	if err != nil {
		return false, err
	}
	for _, notification := range quayNotifications {
		if notification.Title == notificationTile {
			return true, nil
		}
	}
	return false, nil
}
