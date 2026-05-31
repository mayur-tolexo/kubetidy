package usageprofile

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestGroupVersionResource(t *testing.T) {
	got := GroupVersionResource()
	want := schema.GroupVersionResource{
		Group:    "kubetidy.io",
		Version:  "v1alpha1",
		Resource: "usageprofiles",
	}
	if got != want {
		t.Fatalf("GroupVersionResource() = %+v, want %+v", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	orig := UsageProfile{
		Name:      "web-profile",
		Namespace: "prod",
		Spec: Spec{
			TargetRef: TargetRef{Kind: "Deployment", Name: "web"},
		},
		Status: Status{
			ObservedSince: "2026-05-01T00:00:00Z",
			LastUpdated:   "2026-05-31T12:00:00Z",
			SampleCount:   4321,
			WindowSeconds: 86400.5,
			Containers: []ContainerHistory{
				{
					Name: "app",
					CPU: MetricHistory{
						P50:       0.1,
						P95:       0.25,
						Max:       0.5,
						Histogram: "Y3B1LWhpc3RvZ3JhbQ==",
					},
					Memory: MetricHistory{
						P50:       128e6,
						P95:       256e6,
						Max:       512e6,
						Histogram: "bWVtLWhpc3RvZ3JhbQ==",
					},
				},
				{
					Name: "sidecar",
					CPU: MetricHistory{
						P50:       0.01,
						P95:       0.02,
						Max:       0.05,
						Histogram: "c2lkZWNhci1jcHU=",
					},
					Memory: MetricHistory{
						P50:       16e6,
						P95:       32e6,
						Max:       64e6,
						Histogram: "c2lkZWNhci1tZW0=",
					},
				},
			},
		},
	}

	u := orig.ToUnstructured()

	// Sanity-check the on-the-wire shape.
	if u.GetName() != "web-profile" {
		t.Errorf("name = %q, want web-profile", u.GetName())
	}
	if u.GetNamespace() != "prod" {
		t.Errorf("namespace = %q, want prod", u.GetNamespace())
	}
	if u.Object["apiVersion"] != "kubetidy.io/v1alpha1" {
		t.Errorf("apiVersion = %v, want kubetidy.io/v1alpha1", u.Object["apiVersion"])
	}
	if u.Object["kind"] != "UsageProfile" {
		t.Errorf("kind = %v, want UsageProfile", u.Object["kind"])
	}

	got := FromUnstructured(u)

	if !reflect.DeepEqual(got, orig) {
		t.Fatalf("round-trip mismatch:\n got = %+v\nwant = %+v", got, orig)
	}

	// Spot-check the load-bearing fields explicitly (in case DeepEqual masks intent).
	if got.Spec.TargetRef != orig.Spec.TargetRef {
		t.Errorf("targetRef = %+v, want %+v", got.Spec.TargetRef, orig.Spec.TargetRef)
	}
	if got.Status.SampleCount != orig.Status.SampleCount {
		t.Errorf("sampleCount = %d, want %d", got.Status.SampleCount, orig.Status.SampleCount)
	}
	if got.Status.WindowSeconds != orig.Status.WindowSeconds {
		t.Errorf("windowSeconds = %v, want %v", got.Status.WindowSeconds, orig.Status.WindowSeconds)
	}
	if got.Status.ObservedSince != orig.Status.ObservedSince {
		t.Errorf("observedSince = %q, want %q", got.Status.ObservedSince, orig.Status.ObservedSince)
	}
	if got.Status.LastUpdated != orig.Status.LastUpdated {
		t.Errorf("lastUpdated = %q, want %q", got.Status.LastUpdated, orig.Status.LastUpdated)
	}
	if len(got.Status.Containers) != 2 {
		t.Fatalf("got %d containers, want 2", len(got.Status.Containers))
	}
	if got.Status.Containers[0].CPU.Histogram != orig.Status.Containers[0].CPU.Histogram {
		t.Errorf("container[0] cpu histogram = %q, want %q",
			got.Status.Containers[0].CPU.Histogram, orig.Status.Containers[0].CPU.Histogram)
	}
	if got.Status.Containers[1].Memory.Histogram != orig.Status.Containers[1].Memory.Histogram {
		t.Errorf("container[1] mem histogram = %q, want %q",
			got.Status.Containers[1].Memory.Histogram, orig.Status.Containers[1].Memory.Histogram)
	}
}

func TestFromUnstructuredNil(t *testing.T) {
	got := FromUnstructured(nil)
	if !reflect.DeepEqual(got, UsageProfile{}) {
		t.Fatalf("FromUnstructured(nil) = %+v, want zero UsageProfile", got)
	}
}

func TestFromUnstructuredMetadataOnly(t *testing.T) {
	in := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubetidy.io/v1alpha1",
		"kind":       "UsageProfile",
		"metadata": map[string]any{
			"name":      "only-meta",
			"namespace": "default",
		},
	}}

	got := FromUnstructured(in)

	if got.Name != "only-meta" {
		t.Errorf("name = %q, want only-meta", got.Name)
	}
	if got.Namespace != "default" {
		t.Errorf("namespace = %q, want default", got.Namespace)
	}
	if got.Spec != (Spec{}) {
		t.Errorf("spec = %+v, want zero Spec", got.Spec)
	}
	if !reflect.DeepEqual(got.Status, Status{}) {
		t.Errorf("status = %+v, want zero Status", got.Status)
	}
}

func TestFromUnstructuredMalformedContainer(t *testing.T) {
	in := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":      "messy",
			"namespace": "ns",
		},
		"status": map[string]any{
			"sampleCount": int64(7),
			"containers": []any{
				"not-a-map", // malformed: should be skipped without panic
				int64(42),   // malformed: should be skipped without panic
				map[string]any{
					"name": "good",
					"cpu": map[string]any{
						"p50": 0.3,
						"p95": 0.6,
						"max": 1.0,
					},
				},
			},
		},
	}}

	got := FromUnstructured(in)

	if got.Status.SampleCount != 7 {
		t.Errorf("sampleCount = %d, want 7", got.Status.SampleCount)
	}
	if len(got.Status.Containers) != 1 {
		t.Fatalf("got %d containers, want 1 (malformed skipped)", len(got.Status.Containers))
	}
	c := got.Status.Containers[0]
	if c.Name != "good" {
		t.Errorf("container name = %q, want good", c.Name)
	}
	if c.CPU.P50 != 0.3 || c.CPU.P95 != 0.6 || c.CPU.Max != 1.0 {
		t.Errorf("cpu metrics = %+v, want {0.3 0.6 1.0 \"\"}", c.CPU)
	}
	if c.Memory != (MetricHistory{}) {
		t.Errorf("memory = %+v, want zero (absent in input)", c.Memory)
	}
}

func TestFromUnstructuredContainersWrongType(t *testing.T) {
	// "containers" is not a []any — statusFromMap should bail out cleanly.
	in := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "x", "namespace": "y"},
		"status": map[string]any{
			"containers": "not-a-slice",
		},
	}}

	got := FromUnstructured(in)
	if got.Status.Containers != nil {
		t.Errorf("containers = %+v, want nil", got.Status.Containers)
	}
}

func TestFromUnstructuredNumericNormalisation(t *testing.T) {
	// sampleCount as int64, windowSeconds as float64, and metric numbers as int64 —
	// FromUnstructured must read all of them via the float/int normalisers.
	tests := []struct {
		name        string
		sampleCount any
		window      any
		wantSamples int64
		wantWindow  float64
	}{
		{"int64 sampleCount, float64 window", int64(99), float64(3600), 99, 3600},
		{"float64 sampleCount, int64 window", float64(150), int64(7200), 150, 7200},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := &unstructured.Unstructured{Object: map[string]any{
				"metadata": map[string]any{"name": "n", "namespace": "ns"},
				"status": map[string]any{
					"sampleCount":   tc.sampleCount,
					"windowSeconds": tc.window,
					"containers": []any{
						map[string]any{
							"name": "c",
							"cpu": map[string]any{
								"p50": int64(2), // int64 path through nestedFloat
								"p95": 4.5,      // float64 path
								"max": int64(8),
							},
						},
					},
				},
			}}

			got := FromUnstructured(in)
			if got.Status.SampleCount != tc.wantSamples {
				t.Errorf("sampleCount = %d, want %d", got.Status.SampleCount, tc.wantSamples)
			}
			if got.Status.WindowSeconds != tc.wantWindow {
				t.Errorf("windowSeconds = %v, want %v", got.Status.WindowSeconds, tc.wantWindow)
			}
			if len(got.Status.Containers) != 1 {
				t.Fatalf("got %d containers, want 1", len(got.Status.Containers))
			}
			cpu := got.Status.Containers[0].CPU
			if cpu.P50 != 2 || cpu.P95 != 4.5 || cpu.Max != 8 {
				t.Errorf("cpu = %+v, want {2 4.5 8 \"\"}", cpu)
			}
		})
	}
}

func TestNestedAccessorsDefaults(t *testing.T) {
	// Exercise the default branches of the numeric/string normalisers via a status whose
	// numeric fields hold unexpected types — they must fall back to zero, not panic.
	in := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "n", "namespace": "ns"},
		"status": map[string]any{
			"sampleCount":   "not-a-number",
			"windowSeconds": "also-not",
			"observedSince": 12345, // wrong type for string -> ""
		},
	}}

	got := FromUnstructured(in)
	if got.Status.SampleCount != 0 {
		t.Errorf("sampleCount = %d, want 0", got.Status.SampleCount)
	}
	if got.Status.WindowSeconds != 0 {
		t.Errorf("windowSeconds = %v, want 0", got.Status.WindowSeconds)
	}
	if got.Status.ObservedSince != "" {
		t.Errorf("observedSince = %q, want empty", got.Status.ObservedSince)
	}
}
