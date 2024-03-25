package metrics

import (
	"testing"
)

func TestGithubAppAvailabilityProbe(t *testing.T) {
	//
	//t.Run("Should register and record availability metric", func(t *testing.T) {
	//	probe := NewGithubAppAvailabilityProbe(fake.NewClientBuilder().Build())
	//	buildMetrics := NewBuildMetrics([]AvailabilityProbe{probe})
	//	registry := prometheus.NewPedanticRegistry()
	//	err := buildMetrics.InitMetrics(registry)
	//	if err != nil {
	//		t.Errorf("Fail to register metrics: %v", err)
	//	}
	//
	//	buildMetrics.checkProbes(context.Background())
	//
	//	count, err := testutil.GatherAndCount(registry, "redhat_appstudio_buildservice_global_github_app_available")
	//	if err != nil {
	//		t.Errorf("Fail to gather metrics: %v", err)
	//	}
	//
	//	if count != 1 {
	//		t.Errorf("Fail to record metric. Expected 1 got : %v", count)
	//	}
	//})

}
