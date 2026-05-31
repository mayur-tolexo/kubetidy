package usage

import (
	"context"

	"github.com/kubetidy/kubetidy/internal/model"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// metricsServerProvider implements Tier 0 using the metrics.k8s.io API (the data behind
// `kubectl top`). It yields a single live snapshot per container.
type metricsServerProvider struct {
	client metricsv.Interface
}

// NewMetricsServerProvider builds a Tier-0 provider from a metrics clientset.
func NewMetricsServerProvider(client metricsv.Interface) Provider {
	return &metricsServerProvider{client: client}
}

func (p *metricsServerProvider) Name() string            { return "metrics-server" }
func (p *metricsServerProvider) Tier() model.EvidenceTier { return model.TierSnapshot }

// Usage lists PodMetrics matching the workload selector and aggregates per-container usage
// into a snapshot (P50=P95=Max=sampled value, Samples=number of pods).
//
// IMPLEMENTED BY AGENT: see internal/usage task.
func (p *metricsServerProvider) Usage(ctx context.Context, w model.Workload) (map[string]model.UsageStats, error) {
	_ = ctx
	_ = w
	return map[string]model.UsageStats{}, nil
}
