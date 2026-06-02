// Package usageprofile defines the schema and (un)marshalling for the UsageProfile custom
// resource — the kubetidy operator's on-cluster store of per-workload usage history.
//
// To avoid pulling in the controller-runtime / code-generator toolchain (and the dependency
// weight that comes with it), kubetidy does not register a typed scheme. Instead these plain
// Go structs convert to and from *unstructured.Unstructured, which the dynamic client reads
// and writes. The conversion helpers keep that mapping in one well-tested place.
package usageprofile

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Group/version/kind constants for the UsageProfile CRD.
const (
	Group    = "kubetidy.io"
	Version  = "v1alpha1"
	Kind     = "UsageProfile"
	Resource = "usageprofiles"
)

// GroupVersionResource is the dynamic-client coordinate for UsageProfile objects.
func GroupVersionResource() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: Group, Version: Version, Resource: Resource}
}

// ObjectName returns the UsageProfile object name for a workload kind+name. Kubernetes object
// names must be a lowercase RFC 1123 subdomain, so the kind (e.g. "Deployment") is lowercased
// and any character outside [a-z0-9.-] is replaced with '-'. Both the operator (writer) and
// the usage provider (reader) MUST call this so they agree on the name.
func ObjectName(kind, name string) string {
	return sanitizeDNS(strings.ToLower(kind) + "-" + name)
}

// sanitizeDNS coerces s into a valid RFC 1123 subdomain: lowercase, only [a-z0-9.-], and
// trimmed so it starts and ends with an alphanumeric. An empty or fully-invalid input yields
// "x" so the name is always valid.
func sanitizeDNS(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "x"
	}
	return out
}

// apiVersion is the "group/version" string written into every object.
func apiVersion() string { return Group + "/" + Version }

// TargetRef identifies the workload a UsageProfile describes.
type TargetRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// MetricHistory is the recorded usage for one metric (CPU or memory) of one container: the
// summary percentiles plus the encoded decaying-histogram state needed to rehydrate exactly.
type MetricHistory struct {
	Avg float64 `json:"avg,omitempty"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99,omitempty"`
	Max float64 `json:"max"`
	// Histogram is the base64-encoded JSON of histogram.Snapshot, so the operator can resume
	// exact percentile tracking after a restart. Consumers that only need summaries can ignore
	// it and read P50/P95/Max directly.
	Histogram string `json:"histogram,omitempty"`
}

// ContainerHistory holds the recorded history for a single named container.
type ContainerHistory struct {
	Name   string        `json:"name"`
	CPU    MetricHistory `json:"cpu"`
	Memory MetricHistory `json:"memory"`
}

// Status is the operator-maintained state of a UsageProfile.
type Status struct {
	ObservedSince string             `json:"observedSince,omitempty"`
	LastUpdated   string             `json:"lastUpdated,omitempty"`
	SampleCount   int64              `json:"sampleCount,omitempty"`
	WindowSeconds float64            `json:"windowSeconds,omitempty"`
	Containers    []ContainerHistory `json:"containers,omitempty"`
}

// Spec is the small, user/operator-set part of a UsageProfile.
type Spec struct {
	TargetRef TargetRef `json:"targetRef"`
}

// UsageProfile is the typed view of a UsageProfile custom resource.
type UsageProfile struct {
	Name      string
	Namespace string
	Spec      Spec
	Status    Status
}

// ToUnstructured converts a UsageProfile into the *unstructured.Unstructured form the dynamic
// client writes. Status is always included; the caller decides whether to use the status
// subresource when persisting.
func (u *UsageProfile) ToUnstructured() *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": apiVersion(),
		"kind":       Kind,
		"metadata": map[string]any{
			"name":      u.Name,
			"namespace": u.Namespace,
		},
		"spec": map[string]any{
			"targetRef": map[string]any{
				"kind": u.Spec.TargetRef.Kind,
				"name": u.Spec.TargetRef.Name,
			},
		},
		"status": statusToMap(u.Status),
	}
	return &unstructured.Unstructured{Object: obj}
}

// FromUnstructured parses a dynamic-client object into a typed UsageProfile. Missing or
// malformed fields decode to their zero values rather than erroring, so a partially-written or
// older-schema object still yields a usable (if empty) profile.
func FromUnstructured(in *unstructured.Unstructured) UsageProfile {
	up := UsageProfile{}
	if in == nil {
		return up
	}
	up.Name = in.GetName()
	up.Namespace = in.GetNamespace()

	if spec, ok := nestedMap(in.Object, "spec"); ok {
		if ref, ok := nestedMap(spec, "targetRef"); ok {
			up.Spec.TargetRef.Kind = nestedString(ref, "kind")
			up.Spec.TargetRef.Name = nestedString(ref, "name")
		}
	}
	if status, ok := nestedMap(in.Object, "status"); ok {
		up.Status = statusFromMap(status)
	}
	return up
}

// statusToMap renders Status into the generic map form unstructured requires.
func statusToMap(s Status) map[string]any {
	containers := make([]any, 0, len(s.Containers))
	for _, c := range s.Containers {
		containers = append(containers, map[string]any{
			"name":   c.Name,
			"cpu":    metricToMap(c.CPU),
			"memory": metricToMap(c.Memory),
		})
	}
	return map[string]any{
		"observedSince": s.ObservedSince,
		"lastUpdated":   s.LastUpdated,
		"sampleCount":   s.SampleCount,
		"windowSeconds": s.WindowSeconds,
		"containers":    containers,
	}
}

func metricToMap(m MetricHistory) map[string]any {
	return map[string]any{
		"avg":       m.Avg,
		"p50":       m.P50,
		"p95":       m.P95,
		"p99":       m.P99,
		"max":       m.Max,
		"histogram": m.Histogram,
	}
}

// statusFromMap parses the generic status map back into a typed Status.
func statusFromMap(m map[string]any) Status {
	s := Status{
		ObservedSince: nestedString(m, "observedSince"),
		LastUpdated:   nestedString(m, "lastUpdated"),
		SampleCount:   nestedInt64(m, "sampleCount"),
		WindowSeconds: nestedFloat(m, "windowSeconds"),
	}
	raw, ok := m["containers"].([]any)
	if !ok {
		return s
	}
	for _, item := range raw {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		c := ContainerHistory{Name: nestedString(cm, "name")}
		if cpu, ok := nestedMap(cm, "cpu"); ok {
			c.CPU = metricFromMap(cpu)
		}
		if mem, ok := nestedMap(cm, "memory"); ok {
			c.Memory = metricFromMap(mem)
		}
		s.Containers = append(s.Containers, c)
	}
	return s
}

func metricFromMap(m map[string]any) MetricHistory {
	return MetricHistory{
		Avg:       nestedFloat(m, "avg"),
		P50:       nestedFloat(m, "p50"),
		P95:       nestedFloat(m, "p95"),
		P99:       nestedFloat(m, "p99"),
		Max:       nestedFloat(m, "max"),
		Histogram: nestedString(m, "histogram"),
	}
}

// --- small typed accessors over the generic map (unstructured stores numbers as float64 or
// int64 depending on origin, so these normalise both) ---

func nestedMap(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key].(map[string]any)
	return v, ok
}

func nestedString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func nestedFloat(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func nestedInt64(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}
