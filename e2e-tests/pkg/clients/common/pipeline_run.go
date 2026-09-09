package common

import (
	"context"
	"fmt"
	"time"

	applicationApi "github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	"github.com/konflux-ci/e2e-tests/pkg/constants"
	"github.com/onsi/ginkgo/v2"
	pipeline "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"knative.dev/pkg/apis"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Method to get a component pipelinerun using label selector
func (s *SuiteController) GetComponentPipelineRun(componentName, namespace, pipelineType, eventType, sha string) (*pipeline.PipelineRun, error) {
	pipelineRunLabels := map[string]string{"build.konflux-ci.dev/component": componentName}
	if pipelineType != "" {
		pipelineRunLabels["pipelines.konflux-ci.dev/type"] = pipelineType
	}
	if eventType != "" {
		pipelineRunLabels["pipelinesascode.tekton.dev/event-type"] = eventType
	}
	if sha != "" {
		pipelineRunLabels["pipelinesascode.tekton.dev/sha"] = sha
	}
	list := &pipeline.PipelineRunList{}
	err := s.KubeRest().List(context.Background(), list, &crclient.ListOptions{LabelSelector: labels.SelectorFromSet(pipelineRunLabels), Namespace: namespace})
	if err != nil {
		return nil, fmt.Errorf("error listing pipelineruns in %s namespace: %v", namespace, err)
	}
	if len(list.Items) == 1 {
		return &list.Items[0], nil
	}
	if len(list.Items) > 1 {
		return nil, fmt.Errorf("more than one pipelinerun found for component %s", componentName)
	}
	return nil, fmt.Errorf("no pipelinerun found for component %s", componentName)
}

// WaitForComponentPipelineToBeFinished waits for a given component PipelineRun to be finished
func (s *SuiteController) WaitForComponentPipelineToBeFinished(component *applicationApi.Component, pipelineType, eventType, sha string) error {
	pr := &pipeline.PipelineRun{}

	const pipelineCompletionTimeout = 30 * time.Minute

	err := wait.PollUntilContextTimeout(context.Background(), constants.PipelineRunPollingInterval, pipelineCompletionTimeout, true, func(ctx context.Context) (done bool, err error) {
		pr, err = s.GetComponentPipelineRun(component.GetName(), component.GetNamespace(), pipelineType, eventType, sha)

		if err != nil {
			ginkgo.GinkgoWriter.Printf("faile to get pipelinerun for the Component %s/%s\n", component.GetNamespace(), component.GetName())
			return false, nil
		}

		ginkgo.GinkgoWriter.Printf("PipelineRun %s reason: %s\n", pr.Name, pr.GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason())

		if !pr.IsDone() {
			return false, nil
		}

		if pr.GetStatusCondition().GetCondition(apis.ConditionSucceeded).IsTrue() {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	return nil
}
