package v1alpha1

import "testing"

// TestLeafTypeDeepCopy exercises the generated DeepCopy/DeepCopyInto on every non-root type so
// the whole generated file is covered and a future regen that breaks a leaf copy is caught.
// DeepCopy is a pointer method, so each value is bound to a variable to be addressable.
func TestLeafTypeDeepCopy(t *testing.T) {
	tr := TargetRef{Kind: "Deployment", Name: "web"}
	if tr.DeepCopy().Name != "web" {
		t.Error("TargetRef.DeepCopy")
	}
	mh := MetricHistory{P50: 1, P95: 2, Max: 3, Histogram: "h"}
	if mh.DeepCopy().P95 != 2 {
		t.Error("MetricHistory.DeepCopy")
	}
	ch := ContainerHistory{Name: "app"}
	if ch.DeepCopy().Name != "app" {
		t.Error("ContainerHistory.DeepCopy")
	}
	rc := ResourceChange{Container: "app", CPURequestMillicores: 250}
	if rc.DeepCopy().CPURequestMillicores != 250 {
		t.Error("ResourceChange.DeepCopy")
	}
	wt := WorkloadTarget{Namespace: "ns", MonthlySavings: 5}
	if wt.DeepCopy().MonthlySavings != 5 {
		t.Error("WorkloadTarget.DeepCopy")
	}

	ups := UsageProfileSpec{TargetRef: TargetRef{Name: "a"}}
	if ups.DeepCopy().TargetRef.Name != "a" {
		t.Error("UsageProfileSpec.DeepCopy")
	}
	upst := UsageProfileStatus{
		Containers: []ContainerHistory{{Name: "c"}},
		Extensions: map[string]string{"k": "v"},
	}
	if got := upst.DeepCopy(); len(got.Containers) != 1 || got.Extensions["k"] != "v" {
		t.Error("UsageProfileStatus.DeepCopy")
	}

	cuss := ClusterUsageSummarySpec{Scope: "ns"}
	if cuss.DeepCopy().Scope != "ns" {
		t.Error("ClusterUsageSummarySpec.DeepCopy")
	}
	cusst := ClusterUsageSummaryStatus{
		TopTargets: []WorkloadTarget{{Namespace: "ns"}},
		Extensions: map[string]string{"k": "v"},
	}
	if got := cusst.DeepCopy(); len(got.TopTargets) != 1 || got.Extensions["k"] != "v" {
		t.Error("ClusterUsageSummaryStatus.DeepCopy")
	}

	rspec := RecommendationSpec{Source: SourceRules}
	if rspec.DeepCopy().Source != SourceRules {
		t.Error("RecommendationSpec.DeepCopy")
	}
	rst := RecommendationStatus{
		Changes:    []ResourceChange{{Container: "c"}},
		Evidence:   []string{"e"},
		Extensions: map[string]string{"k": "v"},
	}
	if got := rst.DeepCopy(); len(got.Changes) != 1 || len(got.Evidence) != 1 || got.Extensions["k"] != "v" {
		t.Error("RecommendationStatus.DeepCopy")
	}
}
