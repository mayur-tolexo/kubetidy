package usage

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/kubetidy/kubetidy/internal/model"
)

// containerUsage is a compact (container, cpu, mem) triple for table tests. cpu is a
// quantity string ("250m", "1"), mem is a quantity string ("128Mi", "1Gi").
type containerUsage struct {
	name string
	cpu  string
	mem  string
}

// podMetrics builds a PodMetrics object in namespace ns with the given labels.
func podMetrics(name, ns string, lbls map[string]string, containers ...containerUsage) *metricsapi.PodMetrics {
	pm := &metricsapi.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: lbls},
	}
	for _, c := range containers {
		pm.Containers = append(pm.Containers, metricsapi.ContainerMetrics{
			Name: c.name,
			Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(c.cpu),
				corev1.ResourceMemory: resource.MustParse(c.mem),
			},
		})
	}
	return pm
}

// newFakeMetricsClient returns a fake metrics clientset whose PodMetrics List honors the
// namespace and label selector of the request.
//
// We install an explicit list reactor rather than seeding NewSimpleClientset(objs...): the
// k8s.io/metrics v0.32 fake serves PodMetrics under the "pods" resource, and its object
// tracker reliably lists at most one such object (it fails to assemble a multi-item
// PodMetricsList). The reactor exercises the real production Usage path (selector string
// construction via SelectorFromSet, the List call, and per-container aggregation) while
// applying the same label-selector semantics metrics-server applies server-side.
func newFakeMetricsClient(objs ...*metricsapi.PodMetrics) *metricsfake.Clientset {
	client := metricsfake.NewSimpleClientset()
	client.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		la := action.(k8stesting.ListAction)
		ns := la.GetNamespace()
		sel := la.GetListRestrictions().Labels
		if sel == nil {
			sel = labels.Everything()
		}
		out := &metricsapi.PodMetricsList{}
		for _, o := range objs {
			if ns != "" && o.Namespace != ns {
				continue
			}
			if !sel.Matches(labels.Set(o.Labels)) {
				continue
			}
			out.Items = append(out.Items, *o)
		}
		return true, out, nil
	})
	return client
}

func TestMetricsServerProviderUsage(t *testing.T) {
	const ns = "shop"
	selector := map[string]string{"app": "checkout"}

	tests := []struct {
		name      string
		objects   []*metricsapi.PodMetrics
		wantNames map[string]model.UsageStats // expected per-container result
	}{
		{
			name: "two pods, two containers, averaged per container",
			objects: []*metricsapi.PodMetrics{
				podMetrics("checkout-0", ns, selector,
					containerUsage{"api", "200m", "100Mi"},
					containerUsage{"sidecar", "50m", "20Mi"},
				),
				podMetrics("checkout-1", ns, selector,
					containerUsage{"api", "400m", "300Mi"},
					containerUsage{"sidecar", "150m", "60Mi"},
				),
				// A pod that does not match the selector must be excluded.
				podMetrics("other-0", ns, map[string]string{"app": "billing"},
					containerUsage{"api", "9999m", "9Gi"},
				),
				// A matching pod in a different namespace must be excluded.
				podMetrics("checkout-9", "elsewhere", selector,
					containerUsage{"api", "8888m", "8Gi"},
				),
			},
			wantNames: map[string]model.UsageStats{
				"api": {
					// avg cpu = (200+400)/2 = 300m; avg mem = (100+300)/2 = 200Mi
					CPUMillicores: model.Percentiles{P50: 300, P95: 300, Max: 300},
					MemoryBytes:   model.Percentiles{P50: 200 * 1024 * 1024, P95: 200 * 1024 * 1024, Max: 200 * 1024 * 1024},
					Samples:       2,
					Tier:          model.TierSnapshot,
				},
				"sidecar": {
					// avg cpu = (50+150)/2 = 100m; avg mem = (20+60)/2 = 40Mi
					CPUMillicores: model.Percentiles{P50: 100, P95: 100, Max: 100},
					MemoryBytes:   model.Percentiles{P50: 40 * 1024 * 1024, P95: 40 * 1024 * 1024, Max: 40 * 1024 * 1024},
					Samples:       2,
					Tier:          model.TierSnapshot,
				},
			},
		},
		{
			name: "single pod snapshot",
			objects: []*metricsapi.PodMetrics{
				podMetrics("checkout-0", ns, selector, containerUsage{"api", "250m", "128Mi"}),
			},
			wantNames: map[string]model.UsageStats{
				"api": {
					CPUMillicores: model.Percentiles{P50: 250, P95: 250, Max: 250},
					MemoryBytes:   model.Percentiles{P50: 128 * 1024 * 1024, P95: 128 * 1024 * 1024, Max: 128 * 1024 * 1024},
					Samples:       1,
					Tier:          model.TierSnapshot,
				},
			},
		},
		{
			name: "no matching pods yields empty map",
			objects: []*metricsapi.PodMetrics{
				podMetrics("other", ns, map[string]string{"app": "billing"}, containerUsage{"api", "1", "1Gi"}),
			},
			wantNames: map[string]model.UsageStats{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeMetricsClient(tt.objects...)
			p := NewMetricsServerProvider(client)

			if got := p.Name(); got != "metrics-server" {
				t.Errorf("Name() = %q, want metrics-server", got)
			}
			if got := p.Tier(); got != model.TierSnapshot {
				t.Errorf("Tier() = %v, want %v", got, model.TierSnapshot)
			}

			w := model.Workload{Namespace: ns, Selector: selector, Name: "checkout"}
			got, err := p.Usage(context.Background(), w)
			if err != nil {
				t.Fatalf("Usage() error = %v", err)
			}
			if len(got) != len(tt.wantNames) {
				t.Fatalf("Usage() returned %d containers, want %d: %+v", len(got), len(tt.wantNames), got)
			}
			for name, want := range tt.wantNames {
				g, ok := got[name]
				if !ok {
					t.Fatalf("missing container %q in result", name)
				}
				if g.Tier != want.Tier {
					t.Errorf("%s Tier = %v, want %v", name, g.Tier, want.Tier)
				}
				if g.Samples != want.Samples {
					t.Errorf("%s Samples = %d, want %d", name, g.Samples, want.Samples)
				}
				if g.Window != 0 {
					t.Errorf("%s Window = %v, want 0 (snapshot)", name, g.Window)
				}
				if g.CPUMillicores != want.CPUMillicores {
					t.Errorf("%s CPU = %+v, want %+v", name, g.CPUMillicores, want.CPUMillicores)
				}
				if g.MemoryBytes != want.MemoryBytes {
					t.Errorf("%s Mem = %+v, want %+v", name, g.MemoryBytes, want.MemoryBytes)
				}
				// Snapshot invariant: P50 == P95 == Max.
				if g.CPUMillicores.P50 != g.CPUMillicores.P95 || g.CPUMillicores.P95 != g.CPUMillicores.Max {
					t.Errorf("%s CPU percentiles not equal for snapshot: %+v", name, g.CPUMillicores)
				}
			}
		})
	}
}

// TestMetricsServerProviderListError verifies a hard List failure is propagated.
func TestMetricsServerProviderListError(t *testing.T) {
	client := metricsfake.NewSimpleClientset()
	client.PrependReactor("list", "pods", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})
	p := NewMetricsServerProvider(client)
	_, err := p.Usage(context.Background(), model.Workload{Namespace: "shop", Selector: map[string]string{"app": "x"}})
	if err == nil {
		t.Fatal("expected error from failing List, got nil")
	}
}
