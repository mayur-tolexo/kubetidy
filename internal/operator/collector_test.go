package operator

import (
	"context"
	"testing"
	"time"

	"github.com/kubetidy/kubetidy/internal/model"
)

// stepClock returns a Clock that yields base on the first call and advances by step on each
// subsequent call, so successive ticks see a monotonically advancing time.
func stepClock(base time.Time, step time.Duration) Clock {
	cur := base
	first := true
	return func() time.Time {
		if first {
			first = false
			return cur
		}
		cur = cur.Add(step)
		return cur
	}
}

func TestTick_SavesProfileWithUsage(t *testing.T) {
	lister := &fakeLister{workloads: []model.Workload{deployment("ns1", "web", "app")}}
	sampler := &fakeSampler{samples: map[string]Sample{
		"app": {CPUMillicores: 150, MemoryBytes: 256 << 20},
	}}
	store := newFakeStore()

	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	c := NewCollector(lister, sampler, store, stepClock(base, time.Minute))

	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	prof, ok := store.get("ns1", "Deployment-web")
	if !ok {
		t.Fatalf("expected a saved profile for ns1/Deployment-web")
	}
	if prof.Spec.TargetRef.Kind != "Deployment" || prof.Spec.TargetRef.Name != "web" {
		t.Fatalf("TargetRef = %+v, want Deployment/web", prof.Spec.TargetRef)
	}
	if len(prof.Status.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(prof.Status.Containers))
	}
	cont := prof.Status.Containers[0]
	if cont.Name != "app" {
		t.Fatalf("container name = %q, want app", cont.Name)
	}
	if cont.CPU.P95 <= 0 || cont.CPU.Max <= 0 {
		t.Errorf("cpu P95/Max = %v/%v, want > 0", cont.CPU.P95, cont.CPU.Max)
	}
	if cont.Memory.P95 <= 0 || cont.Memory.Max <= 0 {
		t.Errorf("mem P95/Max = %v/%v, want > 0", cont.Memory.P95, cont.Memory.Max)
	}
	if cont.CPU.Max != 150 {
		t.Errorf("cpu Max = %v, want 150 (exact raw max)", cont.CPU.Max)
	}
	if cont.Memory.Max != float64(256<<20) {
		t.Errorf("mem Max = %v, want %v", cont.Memory.Max, float64(256<<20))
	}
	if cont.CPU.Histogram == "" || cont.Memory.Histogram == "" {
		t.Errorf("expected encoded histograms, got cpu=%q mem=%q", cont.CPU.Histogram, cont.Memory.Histogram)
	}
	if prof.Status.SampleCount != 1 {
		t.Errorf("SampleCount = %d, want 1", prof.Status.SampleCount)
	}
	if prof.Status.WindowSeconds < 0 {
		t.Errorf("WindowSeconds = %v, want >= 0", prof.Status.WindowSeconds)
	}
	if prof.Status.LastUpdated == "" {
		t.Errorf("LastUpdated should be set")
	}
}

func TestTick_TwiceGrowsSampleCount(t *testing.T) {
	lister := &fakeLister{workloads: []model.Workload{deployment("ns1", "web", "app")}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 100, MemoryBytes: 1 << 20}}}
	store := newFakeStore()
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	c := NewCollector(lister, sampler, store, stepClock(base, time.Minute))

	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}

	prof, ok := store.get("ns1", "Deployment-web")
	if !ok {
		t.Fatal("no profile saved")
	}
	if prof.Status.SampleCount != 2 {
		t.Errorf("SampleCount after two ticks = %d, want 2", prof.Status.SampleCount)
	}
	if prof.Status.WindowSeconds <= 0 {
		t.Errorf("WindowSeconds = %v, want > 0 after the clock advanced", prof.Status.WindowSeconds)
	}
	if prof.Status.ObservedSince == "" {
		t.Errorf("ObservedSince should be set once a window exists")
	}
}

func TestTick_ListerErrorPropagates(t *testing.T) {
	lister := &fakeLister{err: errBoom}
	sampler := &fakeSampler{}
	store := newFakeStore()
	c := NewCollector(lister, sampler, store, nil)

	if err := c.Tick(context.Background()); err == nil {
		t.Fatal("expected error from lister failure")
	}
	if sampler.calls != 0 {
		t.Errorf("sampler should not be called when lister errors, calls=%d", sampler.calls)
	}
}

func TestTick_SamplerErrorPropagatesButOthersProcessed(t *testing.T) {
	// Two workloads; the sampler errors for "web" only. Tick must return an error but still
	// sample and checkpoint "api".
	lister := &fakeLister{workloads: []model.Workload{
		deployment("ns1", "web", "app"),
		deployment("ns2", "api", "srv"),
	}}
	sampler := &fakeSampler{
		samples: map[string]Sample{"srv": {CPUMillicores: 20, MemoryBytes: 2 << 20}, "app": {CPUMillicores: 10, MemoryBytes: 1 << 20}},
		errFor:  "Deployment/ns1/web",
	}
	store := newFakeStore()
	c := NewCollector(lister, sampler, store, nil)

	if err := c.Tick(context.Background()); err == nil {
		t.Fatal("expected error from sampler failure")
	}
	if _, ok := store.get("ns1", "Deployment-web"); ok {
		t.Error("errored workload should not be saved")
	}
	if _, ok := store.get("ns2", "Deployment-api"); !ok {
		t.Error("non-errored workload should still be saved")
	}
	if sampler.calls != 2 {
		t.Errorf("both workloads should be sampled, calls=%d", sampler.calls)
	}
}

func TestTick_SaveErrorPropagates(t *testing.T) {
	lister := &fakeLister{workloads: []model.Workload{deployment("ns1", "web", "app")}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 10, MemoryBytes: 1 << 20}}}
	store := newFakeStore()
	store.saveErr = errBoom
	c := NewCollector(lister, sampler, store, nil)

	if err := c.Tick(context.Background()); err == nil {
		t.Fatal("expected error from save failure")
	}
}

func TestTick_EmptySamplesSkipped(t *testing.T) {
	lister := &fakeLister{workloads: []model.Workload{deployment("ns1", "web", "app")}}
	sampler := &fakeSampler{samples: map[string]Sample{}} // empty
	store := newFakeStore()
	c := NewCollector(lister, sampler, store, nil)

	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick should not error on empty samples: %v", err)
	}
	if store.saveCount() != 0 {
		t.Errorf("no profile should be saved when there are no samples, saves=%d", store.saveCount())
	}
}

func TestTick_ZeroValuedSamplesNotObservedButProfileSaved(t *testing.T) {
	// A sample with zero CPU and zero memory is still counted (st.observed++) but neither
	// histogram records a value, so Max stays 0. The profile is still saved.
	lister := &fakeLister{workloads: []model.Workload{deployment("ns1", "web", "app")}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 0, MemoryBytes: 0}}}
	store := newFakeStore()
	c := NewCollector(lister, sampler, store, nil)

	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	prof, ok := store.get("ns1", "Deployment-web")
	if !ok {
		t.Fatal("profile should be saved even for zero-valued samples")
	}
	if prof.Status.SampleCount != 1 {
		t.Errorf("SampleCount = %d, want 1", prof.Status.SampleCount)
	}
	if len(prof.Status.Containers) != 1 || prof.Status.Containers[0].CPU.Max != 0 {
		t.Errorf("expected container with zero CPU max, got %+v", prof.Status.Containers)
	}
}

func TestTick_UnlistedContainerNotInProfile(t *testing.T) {
	// The sampler returns a container ("extra") that is NOT in the workload's pod template.
	// buildProfile iterates the template containers, so "extra" is observed in state but does
	// not appear in the saved profile.
	lister := &fakeLister{workloads: []model.Workload{deployment("ns1", "web", "app")}}
	sampler := &fakeSampler{samples: map[string]Sample{
		"app":   {CPUMillicores: 10, MemoryBytes: 1 << 20},
		"extra": {CPUMillicores: 99, MemoryBytes: 9 << 20},
	}}
	store := newFakeStore()
	c := NewCollector(lister, sampler, store, nil)

	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	prof, _ := store.get("ns1", "Deployment-web")
	if len(prof.Status.Containers) != 1 || prof.Status.Containers[0].Name != "app" {
		t.Fatalf("expected only template container 'app', got %+v", prof.Status.Containers)
	}
	// SampleCount only sums state for template containers, so the extra container's count is
	// not included.
	if prof.Status.SampleCount != 1 {
		t.Errorf("SampleCount = %d, want 1 (extra container excluded)", prof.Status.SampleCount)
	}
}

func TestNewCollector_DefaultsClockToNow(t *testing.T) {
	c := NewCollector(&fakeLister{}, &fakeSampler{}, newFakeStore(), nil)
	if c.now == nil {
		t.Fatal("expected default clock to be set when now is nil")
	}
	if got := c.now(); got.IsZero() {
		t.Error("default clock returned zero time")
	}
}

func TestTick_MultiContainerAccumulatesAcrossTicks(t *testing.T) {
	lister := &fakeLister{workloads: []model.Workload{deployment("ns1", "web", "app", "sidecar")}}
	sampler := &fakeSampler{samples: map[string]Sample{
		"app":     {CPUMillicores: 200, MemoryBytes: 300 << 20},
		"sidecar": {CPUMillicores: 5, MemoryBytes: 16 << 20},
	}}
	store := newFakeStore()
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	c := NewCollector(lister, sampler, store, stepClock(base, time.Minute))

	for i := 0; i < 3; i++ {
		if err := c.Tick(context.Background()); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	prof, _ := store.get("ns1", "Deployment-web")
	if len(prof.Status.Containers) != 2 {
		t.Fatalf("want 2 containers, got %d", len(prof.Status.Containers))
	}
	// Two containers x three observations = 6 total samples summed across containers.
	if prof.Status.SampleCount != 6 {
		t.Errorf("SampleCount = %d, want 6", prof.Status.SampleCount)
	}
	for _, cont := range prof.Status.Containers {
		if cont.CPU.Max <= 0 || cont.Memory.Max <= 0 {
			t.Errorf("container %s has non-positive max: cpu=%v mem=%v", cont.Name, cont.CPU.Max, cont.Memory.Max)
		}
	}
}
