package operator

import (
	"context"
	"testing"

	"github.com/kubetidy/kubetidy/api/v1alpha1"
	"github.com/kubetidy/kubetidy/internal/model"
)

// fakeSummaryWriter captures the last ClusterUsageSummary status written.
type fakeSummaryWriter struct {
	last   v1alpha1.ClusterUsageSummaryStatus
	writes int
	err    error
}

func (f *fakeSummaryWriter) SaveSummary(_ context.Context, status v1alpha1.ClusterUsageSummaryStatus) error {
	f.writes++
	if f.err != nil {
		return f.err
	}
	f.last = status
	return nil
}

// fakePricer returns a fixed unit price.
type fakePricer struct{ cpu, mem float64 }

func (f fakePricer) ResourcePrice(_ context.Context, _ model.Workload) (model.ResourcePrice, error) {
	return model.ResourcePrice{CPUCoreMonth: f.cpu, MemGiBMonth: f.mem, Source: "test"}, nil
}

// overProvisionedWorkload is a workload that requests far more than it will be observed using,
// so the rollup sees real savings.
func overProvisionedWorkload(ns, name string) model.Workload {
	return model.Workload{
		Kind:      model.KindDeployment,
		Name:      name,
		Namespace: ns,
		Replicas:  1,
		Selector:  map[string]string{"app": name},
		Containers: []model.Container{{
			Name:     "app",
			Requests: model.ResourceAmounts{CPUMillicores: 2000, MemoryBytes: 4 << 30},
		}},
	}
}

func TestTick_WritesClusterSummary(t *testing.T) {
	w := overProvisionedWorkload("shop", "checkout")
	lister := &fakeLister{workloads: []model.Workload{w}}
	// Observed usage is tiny vs the 2000m / 4Gi request, so there is savings to roll up.
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 50, MemoryBytes: 256 << 20}}}
	store := newFakeStore()
	writer := &fakeSummaryWriter{}

	c := NewCollector(lister, sampler, store, nil).
		WithSummary(writer, fakePricer{cpu: 24, mem: 3.2}, model.DefaultPolicy())

	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if writer.writes != 1 {
		t.Fatalf("summary writes = %d, want 1", writer.writes)
	}
	if writer.last.WorkloadCount != 1 {
		t.Errorf("workloadCount = %d, want 1", writer.last.WorkloadCount)
	}
	if writer.last.WastedMonthlyCost <= 0 {
		t.Errorf("wastedMonthlyCost = %v, want > 0", writer.last.WastedMonthlyCost)
	}
	if len(writer.last.TopTargets) != 1 || writer.last.TopTargets[0].TargetRef.Name != "checkout" {
		t.Errorf("topTargets = %+v, want one for checkout", writer.last.TopTargets)
	}
	if writer.last.GeneratedAt == "" {
		t.Error("summary should carry a GeneratedAt timestamp")
	}
}

func TestTick_SummaryDisabledWhenNoWriter(t *testing.T) {
	w := overProvisionedWorkload("shop", "checkout")
	lister := &fakeLister{workloads: []model.Workload{w}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 50, MemoryBytes: 256 << 20}}}
	// No WithSummary: the collector must not require a writer/pricer.
	c := NewCollector(lister, sampler, newFakeStore(), nil)
	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick without summary: %v", err)
	}
}

func TestTick_SummaryWriteErrorSurfaced(t *testing.T) {
	w := overProvisionedWorkload("shop", "checkout")
	lister := &fakeLister{workloads: []model.Workload{w}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 50, MemoryBytes: 256 << 20}}}
	writer := &fakeSummaryWriter{err: errBoom}

	c := NewCollector(lister, sampler, newFakeStore(), nil).
		WithSummary(writer, fakePricer{cpu: 24, mem: 3.2}, model.DefaultPolicy())

	if err := c.Tick(context.Background()); err == nil {
		t.Fatal("expected the summary write error to surface from Tick")
	}
}
