package operator

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/kubetidy/kubetidy/api/v1alpha1"
	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
	"github.com/kubetidy/kubetidy/internal/histogram"
	"github.com/kubetidy/kubetidy/internal/model"
)

func newDynamicFake(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		usageprofile.GroupVersionResource(): "UsageProfileList",
		v1alpha1.RecommendationGVR:          "RecommendationList",
		v1alpha1.ClusterUsageSummaryGVR:     "ClusterUsageSummaryList",
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
		Status: usageprofile.Status{
			SampleCount: 99,
			// Container history lives in status; it must survive the write (the bug was that
			// Save used a plain Update, which drops the status subresource on a real cluster).
			Containers: []usageprofile.ContainerHistory{
				{Name: "app", CPU: usageprofile.MetricHistory{P95: 250, Max: 300}},
			},
		},
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
	if len(got.Status.Containers) != 1 || got.Status.Containers[0].Name != "app" {
		t.Fatalf("container history not persisted on update: %+v", got.Status.Containers)
	}
}

func sampleRecommendation() v1alpha1.Recommendation {
	rec := v1alpha1.Recommendation{}
	rec.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Recommendation"))
	rec.Name = "deployment-web"
	rec.Namespace = "ns1"
	rec.Spec = v1alpha1.RecommendationSpec{
		TargetRef: v1alpha1.TargetRef{Kind: "Deployment", Name: "web"},
		Namespace: "ns1",
		Source:    v1alpha1.SourceRules,
	}
	rec.Status = v1alpha1.RecommendationStatus{Score: 72, MonthlySavings: 12.5}
	return rec
}

func TestDynamicStore_SaveRecommendationCreates(t *testing.T) {
	client := newDynamicFake()
	store := NewDynamicStore(client)

	if err := store.SaveRecommendation(context.Background(), sampleRecommendation()); err != nil {
		t.Fatalf("SaveRecommendation (create): %v", err)
	}

	got, err := client.Resource(v1alpha1.RecommendationGVR).Namespace("ns1").
		Get(context.Background(), "deployment-web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after create: %v", err)
	}
	spec, _, _ := unstructured.NestedMap(got.Object, "spec")
	if spec["source"] != string(v1alpha1.SourceRules) {
		t.Errorf("spec.source = %v, want rules", spec["source"])
	}
}

func TestDynamicStore_SaveRecommendationUpdatesExisting(t *testing.T) {
	client := newDynamicFake()
	store := NewDynamicStore(client)
	ctx := context.Background()

	if err := store.SaveRecommendation(ctx, sampleRecommendation()); err != nil {
		t.Fatalf("first save: %v", err)
	}

	updated := sampleRecommendation()
	updated.Status.MonthlySavings = 99
	updated.Status.Score = 88
	if err := store.SaveRecommendation(ctx, updated); err != nil {
		t.Fatalf("SaveRecommendation (update): %v", err)
	}

	got, err := client.Resource(v1alpha1.RecommendationGVR).Namespace("ns1").
		Get(ctx, "deployment-web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	status, _, _ := unstructured.NestedMap(got.Object, "status")
	if status["score"] != int64(88) {
		t.Errorf("status.score = %v, want 88", status["score"])
	}
}

func TestDynamicStore_SaveSummaryCreatesThenUpdates(t *testing.T) {
	client := newDynamicFake()
	store := NewDynamicStore(client)
	ctx := context.Background()

	if err := store.SaveSummary(ctx, v1alpha1.ClusterUsageSummaryStatus{WorkloadCount: 3, EfficiencyScore: 60}); err != nil {
		t.Fatalf("SaveSummary (create): %v", err)
	}
	if err := store.SaveSummary(ctx, v1alpha1.ClusterUsageSummaryStatus{WorkloadCount: 4, EfficiencyScore: 75}); err != nil {
		t.Fatalf("SaveSummary (update): %v", err)
	}

	got, err := client.Resource(v1alpha1.ClusterUsageSummaryGVR).Namespace(SummaryNamespace).
		Get(ctx, SummaryName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get cluster summary: %v", err)
	}
	status, _, _ := unstructured.NestedMap(got.Object, "status")
	if status["workloadCount"] != int64(4) {
		t.Errorf("status.workloadCount = %v, want 4", status["workloadCount"])
	}
}

func TestWithHistogramConfig_Overrides(t *testing.T) {
	cpu := histogram.DefaultCPUConfig().WithHalfLife(48 * time.Hour)
	mem := histogram.DefaultMemoryConfig().WithHalfLife(72 * time.Hour)
	c := NewCollector(&fakeLister{}, &fakeSampler{}, newFakeStore(), nil).WithHistogramConfig(cpu, mem)
	if c.cpuCfg.HalfLife != 48*time.Hour {
		t.Errorf("cpu half-life = %v, want 48h", c.cpuCfg.HalfLife)
	}
	if c.memCfg.HalfLife != 72*time.Hour {
		t.Errorf("mem half-life = %v, want 72h", c.memCfg.HalfLife)
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
