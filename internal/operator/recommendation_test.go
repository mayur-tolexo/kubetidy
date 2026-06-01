package operator

import (
	"context"
	"testing"

	"github.com/kubetidy/kubetidy/api/v1alpha1"
	"github.com/kubetidy/kubetidy/internal/model"
)

// fakeRecWriter captures every Recommendation written.
type fakeRecWriter struct {
	saved []v1alpha1.Recommendation
	err   error
}

func (f *fakeRecWriter) SaveRecommendation(_ context.Context, rec v1alpha1.Recommendation) error {
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, rec)
	return nil
}

func TestTick_WritesRecommendations(t *testing.T) {
	w := overProvisionedWorkload("shop", "checkout")
	lister := &fakeLister{workloads: []model.Workload{w}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 50, MemoryBytes: 256 << 20}}}
	writer := &fakeRecWriter{}

	c := NewCollector(lister, sampler, newFakeStore(), nil).
		WithRecommendations(writer, fakePricer{cpu: 24, mem: 3.2}, model.DefaultPolicy())

	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(writer.saved) != 1 {
		t.Fatalf("recommendations written = %d, want 1", len(writer.saved))
	}
	rec := writer.saved[0]
	if rec.Namespace != "shop" {
		t.Errorf("namespace = %q, want shop", rec.Namespace)
	}
	if rec.Name != "deployment-checkout" {
		t.Errorf("name = %q, want deployment-checkout", rec.Name)
	}
	if rec.Spec.Source != v1alpha1.SourceRules {
		t.Errorf("source = %q, want rules", rec.Spec.Source)
	}
	if rec.Spec.TargetRef.Name != "checkout" {
		t.Errorf("targetRef.Name = %q, want checkout", rec.Spec.TargetRef.Name)
	}
	if rec.Status.MonthlySavings <= 0 {
		t.Errorf("savings = %v, want > 0", rec.Status.MonthlySavings)
	}
	if len(rec.Status.Changes) != 1 {
		t.Errorf("changes = %d, want 1 (one container)", len(rec.Status.Changes))
	}
	if rec.Spec.GeneratedAt == "" {
		t.Error("recommendation should carry a GeneratedAt timestamp")
	}
}

func TestTick_RecommendationsDisabledWhenNoWriter(t *testing.T) {
	w := overProvisionedWorkload("shop", "checkout")
	lister := &fakeLister{workloads: []model.Workload{w}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 50, MemoryBytes: 256 << 20}}}
	// No WithRecommendations: the collector must not require a writer.
	c := NewCollector(lister, sampler, newFakeStore(), nil)
	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick without recommendations: %v", err)
	}
}

func TestTick_RecommendationWriteErrorSurfaced(t *testing.T) {
	w := overProvisionedWorkload("shop", "checkout")
	lister := &fakeLister{workloads: []model.Workload{w}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 50, MemoryBytes: 256 << 20}}}
	writer := &fakeRecWriter{err: errBoom}

	c := NewCollector(lister, sampler, newFakeStore(), nil).
		WithRecommendations(writer, fakePricer{cpu: 24, mem: 3.2}, model.DefaultPolicy())

	if err := c.Tick(context.Background()); err == nil {
		t.Fatal("expected the recommendation write error to surface from Tick")
	}
}
