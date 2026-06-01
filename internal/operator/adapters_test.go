package operator

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
	"github.com/kubetidy/kubetidy/internal/model"
)

func newDynamicFake(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		usageprofile.GroupVersionResource(): "UsageProfileList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objs...)
}

func TestConstructors_DoNotPanic(t *testing.T) {
	if NewDynamicStore(nil) == nil {
		t.Error("NewDynamicStore returned nil")
	}
	if NewMetricsSampler(nil) == nil {
		t.Error("NewMetricsSampler returned nil")
	}
	if NewKubeLister(nil) == nil {
		t.Error("NewKubeLister returned nil")
	}
}

func TestDynamicStore_GetNotFoundReturnsAbsent(t *testing.T) {
	client := newDynamicFake()
	store := NewDynamicStore(client)

	_, ok, err := store.Get(context.Background(), "ns1", "Deployment-missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected ok=false for a missing object")
	}
}

func TestDynamicStore_SaveCreatesThenGet(t *testing.T) {
	client := newDynamicFake()
	store := NewDynamicStore(client)

	profile := usageprofile.UsageProfile{
		Namespace: "ns1",
		Name:      "Deployment-web",
		Spec:      usageprofile.Spec{TargetRef: usageprofile.TargetRef{Kind: "Deployment", Name: "web"}},
		Status: usageprofile.Status{
			SampleCount: 5,
			Containers: []usageprofile.ContainerHistory{
				{Name: "app", CPU: usageprofile.MetricHistory{P95: 100, Max: 150}},
			},
		},
	}
	if err := store.Save(context.Background(), profile); err != nil {
		t.Fatalf("Save (create): %v", err)
	}

	got, ok, err := store.Get(context.Background(), "ns1", "Deployment-web")
	if err != nil {
		t.Fatalf("Get after create: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after create")
	}
	if got.Status.SampleCount != 5 {
		t.Errorf("SampleCount = %d, want 5", got.Status.SampleCount)
	}
	if got.Spec.TargetRef.Name != "web" {
		t.Errorf("TargetRef.Name = %q, want web", got.Spec.TargetRef.Name)
	}
	if len(got.Status.Containers) != 1 || got.Status.Containers[0].Name != "app" {
		t.Errorf("containers not round-tripped: %+v", got.Status.Containers)
	}
}

func TestDynamicStore_SaveUpdatesExisting(t *testing.T) {
	existing := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": usageprofile.Group + "/" + usageprofile.Version,
		"kind":       usageprofile.Kind,
		"metadata": map[string]any{
			"namespace":       "ns1",
			"name":            "Deployment-web",
			"resourceVersion": "1",
		},
		"spec":   map[string]any{"targetRef": map[string]any{"kind": "Deployment", "name": "web"}},
		"status": map[string]any{"sampleCount": int64(1)},
	}}
	client := newDynamicFake(existing)
	store := NewDynamicStore(client)

	profile := usageprofile.UsageProfile{
		Namespace: "ns1",
		Name:      "Deployment-web",
		Status:    usageprofile.Status{SampleCount: 99},
	}
	if err := store.Save(context.Background(), profile); err != nil {
		t.Fatalf("Save (update): %v", err)
	}

	got, ok, err := store.Get(context.Background(), "ns1", "Deployment-web")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if !ok || got.Status.SampleCount != 99 {
		t.Fatalf("update did not persist, ok=%v profile=%+v", ok, got)
	}
}

func TestMetricsSampler_EmptySelectorShortCircuits(t *testing.T) {
	// No selector -> empty map, never touches the client.
	s := NewMetricsSampler(nil)
	got, err := s.Sample(context.Background(), model.Workload{
		Kind:      model.KindDeployment,
		Name:      "web",
		Namespace: "ns1",
	})
	if err != nil {
		t.Fatalf("Sample with empty selector: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty sample map, got %+v", got)
	}
}

func TestMetricsSampler_NoMatchingPods(t *testing.T) {
	client := metricsfake.NewSimpleClientset()
	s := NewMetricsSampler(client)
	got, err := s.Sample(context.Background(), model.Workload{
		Kind:      model.KindDeployment,
		Name:      "web",
		Namespace: "ns1",
		Selector:  map[string]string{"app": "web"},
	})
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no samples, got %+v", got)
	}
}
