package usage

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
	"github.com/kubetidy/kubetidy/internal/model"
)

// operatorProvider implements the primary "no Prometheus required" tier (model.TierOperator)
// by reading UsageProfile CRDs that the kubetidy operator maintains. Each profile already
// holds decayed P50/P95/max per container, so this provider is a thin read-and-map: it does
// not compute anything itself.
type operatorProvider struct {
	client dynamic.Interface
}

// NewOperatorProvider builds a Tier-0 provider backed by the dynamic client. It reads
// UsageProfile objects written by the kubetidy operator.
func NewOperatorProvider(client dynamic.Interface) Provider {
	return &operatorProvider{client: client}
}

func (p *operatorProvider) Name() string             { return "kubetidy operator" }
func (p *operatorProvider) Tier() model.EvidenceTier { return model.TierOperator }

// Usage fetches the UsageProfile for the workload and maps its recorded history to per-container
// UsageStats. A missing profile (the operator has not yet observed this workload) yields an
// empty map rather than an error, so the scan degrades gracefully for not-yet-profiled
// workloads.
func (p *operatorProvider) Usage(ctx context.Context, w model.Workload) (map[string]model.UsageStats, error) {
	name := usageprofile.ObjectName(string(w.Kind), w.Name)
	obj, err := p.client.
		Resource(usageprofile.GroupVersionResource()).
		Namespace(w.Namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		// Not-found and transient errors both degrade to "no data for this workload"; the
		// engine records a warning and moves on rather than failing the whole scan.
		return map[string]model.UsageStats{}, nil
	}

	profile := usageprofile.FromUnstructured(obj)
	window := time.Duration(profile.Status.WindowSeconds * float64(time.Second))

	out := make(map[string]model.UsageStats, len(profile.Status.Containers))
	for _, c := range profile.Status.Containers {
		out[c.Name] = model.UsageStats{
			CPUMillicores: model.Percentiles{Avg: c.CPU.Avg, P50: c.CPU.P50, P95: c.CPU.P95, P99: c.CPU.P99, Max: c.CPU.Max},
			MemoryBytes:   model.Percentiles{Avg: c.Memory.Avg, P50: c.Memory.P50, P95: c.Memory.P95, P99: c.Memory.P99, Max: c.Memory.Max},
			Window:        window,
			Samples:       profile.Status.SampleCount,
			Tier:          model.TierOperator,
		}
	}
	return out, nil
}

// DetectOperator reports whether the kubetidy operator is installed and has written at least
// one UsageProfile, so the CLI can auto-select the operator tier. It is best-effort: any error
// (CRD not installed, RBAC, transient) means "operator not available" and the caller falls back
// to the metrics-server snapshot tier.
func DetectOperator(client dynamic.Interface) bool {
	if client == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	list, err := client.
		Resource(usageprofile.GroupVersionResource()).
		Namespace(metav1.NamespaceAll).
		List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil || list == nil {
		return false
	}
	return len(list.Items) > 0
}
