package usage

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/kubetidy/kubetidy/internal/model"
)

// metricsServerProvider implements Tier 0 using the metrics.k8s.io API (the data behind
// `kubectl top`). It yields a single live snapshot per container — no Prometheus required,
// which is kubetidy's "works on any cluster, instantly" entry point.
type metricsServerProvider struct {
	client metricsv.Interface
}

// NewMetricsServerProvider builds a Tier-0 provider from a metrics clientset.
func NewMetricsServerProvider(client metricsv.Interface) Provider {
	return &metricsServerProvider{client: client}
}

func (p *metricsServerProvider) Name() string             { return "metrics-server" }
func (p *metricsServerProvider) Tier() model.EvidenceTier { return model.TierSnapshot }

// containerAccumulator sums per-container usage across the matching pods so we can average.
type containerAccumulator struct {
	cpuMillis float64
	memBytes  float64
	pods      int64
}

// Usage lists PodMetrics matching the workload selector and aggregates per-container usage
// into a snapshot. Because this is a single point in time, the per-container average across
// matching pods is reported as P50 == P95 == Max, Samples is the number of contributing
// pods, and Window is 0. Pods not matching the workload selector are excluded.
func (p *metricsServerProvider) Usage(ctx context.Context, w model.Workload) (map[string]model.UsageStats, error) {
	selector := labels.SelectorFromSet(w.Selector).String()
	list, err := p.client.MetricsV1beta1().PodMetricses(w.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, err
	}

	acc := make(map[string]*containerAccumulator)
	for i := range list.Items {
		pod := &list.Items[i]
		for j := range pod.Containers {
			c := &pod.Containers[j]
			a := acc[c.Name]
			if a == nil {
				a = &containerAccumulator{}
				acc[c.Name] = a
			}
			if cpu, ok := c.Usage[corev1.ResourceCPU]; ok {
				a.cpuMillis += float64(cpu.MilliValue())
			}
			if mem, ok := c.Usage[corev1.ResourceMemory]; ok {
				a.memBytes += float64(mem.Value())
			}
			a.pods++
		}
	}

	out := make(map[string]model.UsageStats, len(acc))
	for name, a := range acc {
		if a.pods == 0 {
			continue
		}
		cpu := a.cpuMillis / float64(a.pods)
		mem := a.memBytes / float64(a.pods)
		out[name] = model.UsageStats{
			// A single snapshot has no distribution — every statistic is the one reading.
			CPUMillicores: model.Percentiles{Avg: cpu, P50: cpu, P95: cpu, P99: cpu, Max: cpu},
			MemoryBytes:   model.Percentiles{Avg: mem, P50: mem, P95: mem, P99: mem, Max: mem},
			Window:        0,
			Samples:       a.pods,
			Tier:          model.TierSnapshot,
		}
	}
	return out, nil
}
