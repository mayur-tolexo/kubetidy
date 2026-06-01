package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestAddToSchemeRegistersTypes(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	for _, kind := range []string{"UsageProfile", "ClusterUsageSummary", "Recommendation"} {
		gvk := GroupVersion.WithKind(kind)
		if !s.Recognizes(gvk) {
			t.Errorf("scheme does not recognize %s", gvk)
		}
		if !s.Recognizes(GroupVersion.WithKind(kind + "List")) {
			t.Errorf("scheme does not recognize %sList", kind)
		}
	}
}

func TestGroupVersionResources(t *testing.T) {
	cases := map[string]string{
		UsageProfileGVR.Resource:        "usageprofiles",
		ClusterUsageSummaryGVR.Resource: "clusterusagesummaries",
		RecommendationGVR.Resource:      "recommendations",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("GVR resource = %q, want %q", got, want)
		}
	}
	if GroupVersion.Group != "kubetidy.io" || GroupVersion.Version != "v1alpha1" {
		t.Errorf("GroupVersion = %s, want kubetidy.io/v1alpha1", GroupVersion)
	}
}

func TestDeepCopyRoundTrips(t *testing.T) {
	up := &UsageProfile{
		Spec: UsageProfileSpec{TargetRef: TargetRef{Kind: "Deployment", Name: "web"}},
		Status: UsageProfileStatus{
			SampleCount: 42,
			Containers: []ContainerHistory{
				{Name: "app", CPU: MetricHistory{P95: 280}, Memory: MetricHistory{Max: 1 << 30}},
			},
			Extensions: map[string]string{"k": "v"},
		},
	}
	clone := up.DeepCopy()
	if clone == up {
		t.Fatal("DeepCopy returned the same pointer")
	}
	// Mutating the clone must not affect the original (proves deep, not shallow, copy).
	clone.Status.Containers[0].Name = "changed"
	clone.Status.Extensions["k"] = "mutated"
	if up.Status.Containers[0].Name != "app" || up.Status.Extensions["k"] != "v" {
		t.Error("DeepCopy is shallow: mutating the clone changed the original")
	}

	// DeepCopyObject returns a runtime.Object of the same kind.
	if _, ok := up.DeepCopyObject().(*UsageProfile); !ok {
		t.Error("DeepCopyObject did not return *UsageProfile")
	}
}

// TestDeepCopyAllTypes exercises the generated DeepCopy/DeepCopyObject for every type
// (including list types and nested slices/maps) so a shallow-copy regression is caught.
func TestDeepCopyAllTypes(t *testing.T) {
	rec := &Recommendation{
		Spec: RecommendationSpec{TargetRef: TargetRef{Kind: "Deployment", Name: "api"}, Source: SourceLLM},
		Status: RecommendationStatus{
			Score:      90,
			Changes:    []ResourceChange{{Container: "api", CPURequestMillicores: 250, MemoryRequestBytes: 1 << 20}},
			Evidence:   []string{"p95 280m"},
			Extensions: map[string]string{"model": "x"},
		},
	}
	recClone := rec.DeepCopy()
	recClone.Status.Changes[0].Container = "changed"
	recClone.Status.Evidence[0] = "changed"
	recClone.Status.Extensions["model"] = "changed"
	if rec.Status.Changes[0].Container != "api" || rec.Status.Evidence[0] != "p95 280m" || rec.Status.Extensions["model"] != "x" {
		t.Error("Recommendation.DeepCopy is shallow")
	}
	if _, ok := rec.DeepCopyObject().(*Recommendation); !ok {
		t.Error("Recommendation.DeepCopyObject wrong type")
	}

	cus := &ClusterUsageSummary{
		Status: ClusterUsageSummaryStatus{
			TopTargets: []WorkloadTarget{{TargetRef: TargetRef{Kind: "Deployment", Name: "web"}, MonthlySavings: 12}},
			Extensions: map[string]string{"k": "v"},
		},
	}
	cusClone := cus.DeepCopy()
	cusClone.Status.TopTargets[0].MonthlySavings = 999
	if cus.Status.TopTargets[0].MonthlySavings != 12 {
		t.Error("ClusterUsageSummary.DeepCopy is shallow")
	}
	if _, ok := cus.DeepCopyObject().(*ClusterUsageSummary); !ok {
		t.Error("ClusterUsageSummary.DeepCopyObject wrong type")
	}

	// List types.
	upl := &UsageProfileList{Items: []UsageProfile{{Spec: UsageProfileSpec{TargetRef: TargetRef{Name: "a"}}}}}
	if got := upl.DeepCopy(); got == upl || len(got.Items) != 1 {
		t.Error("UsageProfileList.DeepCopy failed")
	}
	if _, ok := upl.DeepCopyObject().(*UsageProfileList); !ok {
		t.Error("UsageProfileList.DeepCopyObject wrong type")
	}
	recl := &RecommendationList{Items: []Recommendation{*rec}}
	if _, ok := recl.DeepCopyObject().(*RecommendationList); !ok {
		t.Error("RecommendationList.DeepCopyObject wrong type")
	}
	cusl := &ClusterUsageSummaryList{Items: []ClusterUsageSummary{*cus}}
	if _, ok := cusl.DeepCopyObject().(*ClusterUsageSummaryList); !ok {
		t.Error("ClusterUsageSummaryList.DeepCopyObject wrong type")
	}
}
