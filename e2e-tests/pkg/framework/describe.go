package framework

import (
	ginkgo "github.com/onsi/ginkgo/v2"
)

func BuildSuiteDescribe(text string, args ...interface{}) bool {
	return ginkgo.Describe("[build-service-suite "+text+"]", args)
}

func ImageControllerSuiteDescribe(text string, args ...interface{}) bool {
	return ginkgo.Describe("[image-controller-suite "+text+"]", args)
}

func ContainerBuildCatalogSuiteDescribe(text string, args ...interface{}) bool {
	return ginkgo.Describe("[container-build-catalog-suite "+text+"]", args)
}
