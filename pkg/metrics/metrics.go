package metrics

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
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

func NewBuildMetrics(probes []AvailabilityProbe) *BuildMetrics {
	return &BuildMetrics{probes: probes}
}

func (m *BuildMetrics) InitMetrics(registerer prometheus.Registerer) error {
	for _, probe := range m.probes {
		if err := registerer.Register(probe.AvailabilityGauge()); err != nil {
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
				m.checkProbes(ctx)
			}
		}
	}()
}

func (m *BuildMetrics) checkProbes(ctx context.Context) {
	for _, probe := range m.probes {
		pingErr := probe.CheckAvailability(ctx)
		if pingErr != nil {
			log := ctrllog.FromContext(ctx)
			log.Error(pingErr, "Error checking availability probe", "probe", probe)
			probe.AvailabilityGauge().Set(0)
		} else {
			probe.AvailabilityGauge().Set(1)
		}
	}
}

// AvailabilityProbe represents a probe that checks the availability of a certain aspects of the service
type AvailabilityProbe interface {
	CheckAvailability(ctx context.Context) error
	AvailabilityGauge() prometheus.Gauge
}
