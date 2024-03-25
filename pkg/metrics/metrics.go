package metrics

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"time"
)

const (
	MetricsNamespace          = "redhat_appstudio"
	MetricsSubsystem          = "buildservice"
	BuildServiceNamespaceName = "build-service"
)

// BuildMetrics represents a collection of metrics to be registered on a
// Prometheus metrics registry for a build service.
type BuildMetrics struct {
	probes []AvailabilityProbe
}

func NewBuildMetrics(client client.Client) *BuildMetrics {

	checker := &GithubAppAvailabilityChecker{
		client: client,
	}

	return &BuildMetrics{probes: []AvailabilityProbe{
		{
			checkAvailability: checker.check,
			availabilityMetric: prometheus.NewGauge(
				prometheus.GaugeOpts{
					Namespace: MetricsNamespace,
					Subsystem: MetricsSubsystem,
					Name:      "global_github_app_available",
					Help:      "The availability of the Github App",
				}),
		}}}

}

func (m *BuildMetrics) InitMetrics() error {

	for _, probe := range m.probes {
		if err := metrics.Registry.Register(probe.availabilityMetric); err != nil {
			return fmt.Errorf("failed to register the availability metric: %w", err)
		}
	}

	return nil
}
func (m *BuildMetrics) StartMetrics(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	log := ctrllog.FromContext(ctx)
	log.Info("Starting metrics")
	go func() {
		for {
			select {
			case <-ctx.Done(): // Shutdown if context is canceled
				log.Info("Shutting down metrics")
				ticker.Stop()
				return
			case <-ticker.C:
				for _, probe := range m.probes {
					pingErr := probe.checkAvailability(ctx)
					if pingErr != nil {
						log.Error(pingErr, "Error checking connection", "probe", probe)
						probe.availabilityMetric.Set(0)
					} else {
						probe.availabilityMetric.Set(1)
					}

				}

			}
		}
	}()
}

// AvailabilityProbe represents a probe that checks the availability of a certain aspects of the service
type AvailabilityProbe struct {
	checkAvailability  func(ctx context.Context) error
	availabilityMetric prometheus.Gauge
}
