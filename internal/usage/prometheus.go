package usage

import (
	"context"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"github.com/kubetidy/kubetidy/internal/model"
)

// prometheusProvider implements Tier 1 using historical percentiles from Prometheus
// (container_cpu_usage_seconds_total / container_memory_working_set_bytes via cAdvisor +
// kube-state-metrics), over a configurable window.
type prometheusProvider struct {
	api    promv1.API
	window string // e.g. "14d"
}

// NewPrometheusProvider builds a Tier-1 provider from a Prometheus base URL.
//
// IMPLEMENTED BY AGENT: wire promapi.NewClient + promv1.NewAPI; validate window.
func NewPrometheusProvider(baseURL, window string) (Provider, error) {
	client, err := promapi.NewClient(promapi.Config{Address: baseURL})
	if err != nil {
		return nil, err
	}
	return &prometheusProvider{api: promv1.NewAPI(client), window: window}, nil
}

func (p *prometheusProvider) Name() string             { return "prometheus" }
func (p *prometheusProvider) Tier() model.EvidenceTier { return model.TierHistorical }

// Usage runs percentile queries for CPU and memory per container over the window.
//
// IMPLEMENTED BY AGENT: see internal/usage task.
func (p *prometheusProvider) Usage(ctx context.Context, w model.Workload) (map[string]model.UsageStats, error) {
	_ = ctx
	_ = w
	return map[string]model.UsageStats{}, nil
}
