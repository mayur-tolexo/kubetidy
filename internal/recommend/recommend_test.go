package recommend

import (
	"testing"
	"time"

	"github.com/kubetidy/kubetidy/api/v1alpha1"
	"github.com/kubetidy/kubetidy/internal/model"
)

func rec(kind, ns, name, container string, savings float64, conf float64, cpu, mem int64, evidence string) model.Recommendation {
	return model.Recommendation{
		Workload: model.Workload{
			Kind:      model.WorkloadKind(kind),
			Name:      name,
			Namespace: ns,
		},
		ContainerName: container,
		Proposed: model.ResourceSpec{
			Requests: model.ResourceAmounts{CPUMillicores: cpu, MemoryBytes: mem},
		},
		MonthlySavings: savings,
		Confidence:     model.Confidence{Score: conf},
		Evidence:       evidence,
	}
}

func TestBuild_OnePerWorkloadAggregatesContainers(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	recs := []model.Recommendation{
		rec("Deployment", "shop", "api", "app", 30, 0.9, 200, 256<<20, "p95 cpu 180m"),
		rec("Deployment", "shop", "api", "sidecar", 10, 0.5, 50, 64<<20, "p95 cpu 40m"),
	}

	built := Build(recs, Options{GeneratedAt: at})
	if len(built) != 1 {
		t.Fatalf("want 1 workload recommendation, got %d", len(built))
	}
	b := built[0]

	if b.Namespace != "shop" {
		t.Errorf("namespace = %q, want shop", b.Namespace)
	}
	// Name shares the UsageProfile scheme: lowercase <kind>-<name>.
	if b.Name != "deployment-api" {
		t.Errorf("name = %q, want deployment-api", b.Name)
	}

	r := b.Recommendation
	if r.Spec.Source != v1alpha1.SourceRules {
		t.Errorf("source = %q, want rules (default)", r.Spec.Source)
	}
	if r.Spec.GeneratedAt != at.Format(time.RFC3339) {
		t.Errorf("generatedAt = %q, want %q", r.Spec.GeneratedAt, at.Format(time.RFC3339))
	}
	if r.Spec.TargetRef.Kind != "Deployment" || r.Spec.TargetRef.Name != "api" {
		t.Errorf("targetRef = %+v, want Deployment/api", r.Spec.TargetRef)
	}
	if r.Spec.InputsRef != "deployment-api" {
		t.Errorf("inputsRef = %q, want deployment-api", r.Spec.InputsRef)
	}

	if got := r.Status.MonthlySavings; got != 40 {
		t.Errorf("savings = %v, want 40 (summed)", got)
	}
	if got := r.Status.Confidence; got != 90 {
		t.Errorf("confidence = %d, want 90 (max)", got)
	}
	if len(r.Status.Changes) != 2 {
		t.Fatalf("changes = %d, want 2 (one per container)", len(r.Status.Changes))
	}
	if len(r.Status.Evidence) != 2 {
		t.Errorf("evidence lines = %d, want 2", len(r.Status.Evidence))
	}
	if r.Status.Score <= 0 {
		t.Errorf("score = %d, want > 0 for a positive-savings workload", r.Status.Score)
	}
}

func TestBuild_PreservesFirstSeenOrderAcrossWorkloads(t *testing.T) {
	recs := []model.Recommendation{
		rec("Deployment", "a", "second", "c", 5, 0.5, 10, 10, ""),
		rec("StatefulSet", "a", "first", "c", 5, 0.5, 10, 10, ""),
	}
	built := Build(recs, Options{})
	if len(built) != 2 {
		t.Fatalf("want 2, got %d", len(built))
	}
	if built[0].Name != "deployment-second" || built[1].Name != "statefulset-first" {
		t.Errorf("order not preserved: %q, %q", built[0].Name, built[1].Name)
	}
}

func TestBuild_NoTimestampWhenZero(t *testing.T) {
	built := Build([]model.Recommendation{rec("Deployment", "a", "x", "c", 1, 0.5, 10, 10, "")}, Options{})
	if got := built[0].Recommendation.Spec.GeneratedAt; got != "" {
		t.Errorf("generatedAt = %q, want empty for zero time", got)
	}
}

func TestBuild_ExplicitSourceHonored(t *testing.T) {
	built := Build([]model.Recommendation{rec("Deployment", "a", "x", "c", 1, 0.5, 10, 10, "")},
		Options{Source: v1alpha1.SourceLLM})
	if got := built[0].Recommendation.Spec.Source; got != v1alpha1.SourceLLM {
		t.Errorf("source = %q, want llm", got)
	}
}

func TestScoreFor(t *testing.T) {
	cases := []struct {
		name     string
		savings  float64
		conf     int
		wantZero bool
		min, max int
	}{
		{"no savings scores zero", 0, 100, true, 0, 0},
		{"negative savings scores zero", -50, 100, true, 0, 0},
		{"100/mo full confidence ~50", 100, 100, false, 45, 55},
		{"400/mo full confidence ~80", 400, 100, false, 75, 85},
		{"low confidence dampens", 400, 25, false, 15, 25},
		{"score never exceeds 100", 1_000_000, 100, false, 95, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreFor(tc.savings, tc.conf)
			if tc.wantZero && got != 0 {
				t.Fatalf("score = %d, want 0", got)
			}
			if got < tc.min || got > tc.max {
				t.Errorf("score = %d, want in [%d,%d]", got, tc.min, tc.max)
			}
		})
	}
}
