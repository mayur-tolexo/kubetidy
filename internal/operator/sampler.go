package operator

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/kubetidy/kubetidy/internal/model"
)

// metricsSampler is the production Sampler: it reads current per-container usage from
// metrics-server (the metrics.k8s.io API) for the pods matching a workload's selector, and
// averages across replicas so one Sample represents the typical pod.
type metricsSampler struct {
	client metricsv.Interface
}

// NewMetricsSampler builds a Sampler backed by metrics-server.
func NewMetricsSampler(client metricsv.Interface) Sampler {
	return &metricsSampler{client: client}
}

// Sample returns the current average per-container usage across the workload's matching pods.
// An empty selector or no matching pods yields an empty map (nothing to record this tick).
func (s *metricsSampler) Sample(ctx context.Context, w model.Workload) (map[string]Sample, error) {
	if len(w.Selector) == 0 {
		return map[string]Sample{}, nil
	}
	selector := labels.SelectorFromSet(w.Selector).String()
	list, err := s.client.MetricsV1beta1().PodMetricses(w.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, err
	}

	type acc struct {
		cpu  float64
		mem  float64
		pods float64
	}
	totals := make(map[string]*acc)
	for i := range list.Items {
		pod := &list.Items[i]
		for j := range pod.Containers {
			c := &pod.Containers[j]
			a := totals[c.Name]
			if a == nil {
				a = &acc{}
				totals[c.Name] = a
			}
			if cpu, ok := c.Usage[corev1.ResourceCPU]; ok {
				a.cpu += float64(cpu.MilliValue())
			}
			if mem, ok := c.Usage[corev1.ResourceMemory]; ok {
				a.mem += float64(mem.Value())
			}
			a.pods++
		}
	}

	out := make(map[string]Sample, len(totals))
	for name, a := range totals {
		if a.pods == 0 {
			continue
		}
		out[name] = Sample{CPUMillicores: a.cpu / a.pods, MemoryBytes: a.mem / a.pods}
	}
	return out, nil
}
