package operator

import (
	"context"
	"testing"
	"time"

	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
	"github.com/kubetidy/kubetidy/internal/model"
)

func TestRehydrate_SeedsStateAcrossRestart(t *testing.T) {
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{workloads: []model.Workload{deployment("ns1", "web", "app")}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 100, MemoryBytes: 1 << 20}}}
	store := newFakeStore()

	// First collector accumulates 3 samples and checkpoints them.
	c1 := NewCollector(lister, sampler, store, stepClock(base, time.Minute))
	for i := 0; i < 3; i++ {
		if err := c1.Tick(context.Background()); err != nil {
			t.Fatalf("c1 Tick %d: %v", i, err)
		}
	}
	prof, _ := store.get("ns1", "deployment-web")
	if prof.Status.SampleCount != 3 {
		t.Fatalf("setup: SampleCount = %d, want 3", prof.Status.SampleCount)
	}

	// A brand-new collector rehydrates from the persisted profile, then ticks once more.
	c2 := NewCollector(lister, sampler, store, stepClock(base.Add(time.Hour), time.Minute))
	c2.Rehydrate(context.Background(), lister.workloads)

	if err := c2.Tick(context.Background()); err != nil {
		t.Fatalf("c2 Tick: %v", err)
	}
	prof2, _ := store.get("ns1", "deployment-web")
	if prof2.Status.SampleCount != 4 {
		t.Errorf("SampleCount after rehydrate+tick = %d, want 4 (3 prior + 1 new)", prof2.Status.SampleCount)
	}
	// Histogram state preserved: the container is still present with usage.
	if len(prof2.Status.Containers) != 1 || prof2.Status.Containers[0].CPU.Max <= 0 {
		t.Errorf("expected preserved container usage, got %+v", prof2.Status.Containers)
	}
	// The observed-since window should reflect prior accumulation (it parsed the persisted
	// ObservedSince, which predates this collector's clock), so WindowSeconds is large.
	if prof2.Status.WindowSeconds <= 0 {
		t.Errorf("WindowSeconds = %v, want > 0 after rehydrate", prof2.Status.WindowSeconds)
	}
}

func TestRehydrate_MissingProfileStartsFresh(t *testing.T) {
	store := newFakeStore() // empty: Get returns ok=false
	c := NewCollector(&fakeLister{}, &fakeSampler{}, store, nil)
	c.Rehydrate(context.Background(), []model.Workload{deployment("ns1", "absent", "app")})
	if len(c.state) != 0 {
		t.Errorf("no state should be seeded for a missing profile, got %d entries", len(c.state))
	}
}

func TestRehydrate_StoreErrorSkipped(t *testing.T) {
	store := newFakeStore()
	store.getErr = errBoom
	c := NewCollector(&fakeLister{}, &fakeSampler{}, store, nil)
	// Best-effort: a Get error simply skips that workload without panicking.
	c.Rehydrate(context.Background(), []model.Workload{deployment("ns1", "web", "app")})
	if len(c.state) != 0 {
		t.Errorf("no state should be seeded when Get errors, got %d entries", len(c.state))
	}
}

func TestRehydrate_InvalidHistogramFallsBackButKeepsCount(t *testing.T) {
	// A persisted profile whose histogram strings are garbage rehydrates with a fresh (empty)
	// histogram but still carries the SampleCount forward.
	store := newFakeStore()
	prof := usageprofile.UsageProfile{
		Namespace: "ns1",
		Name:      "deployment-web",
		Status: usageprofile.Status{
			SampleCount:   7,
			ObservedSince: "2026-05-30T00:00:00Z",
			Containers: []usageprofile.ContainerHistory{
				{Name: "app", CPU: usageprofile.MetricHistory{Histogram: "!!!not-base64!!!"}, Memory: usageprofile.MetricHistory{Histogram: ""}},
			},
		},
	}
	if err := store.Save(context.Background(), prof); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{workloads: []model.Workload{deployment("ns1", "web", "app")}}
	sampler := &fakeSampler{samples: map[string]Sample{"app": {CPUMillicores: 50, MemoryBytes: 2 << 20}}}
	c := NewCollector(lister, sampler, store, stepClock(base, time.Minute))
	c.Rehydrate(context.Background(), lister.workloads)

	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick after rehydrate: %v", err)
	}
	prof2, _ := store.get("ns1", "deployment-web")
	// 7 carried over + 1 new observation.
	if prof2.Status.SampleCount != 8 {
		t.Errorf("SampleCount = %d, want 8 (7 seeded + 1 new)", prof2.Status.SampleCount)
	}
	// The fresh fallback histogram only saw the single new observation, so its max is the new
	// value, not anything from the corrupt snapshot.
	if prof2.Status.Containers[0].CPU.Max != 50 {
		t.Errorf("CPU Max = %v, want 50 (fresh histogram + one new sample)", prof2.Status.Containers[0].CPU.Max)
	}
	// ObservedSince parsed from the persisted profile precedes the tick clock, so the window
	// spans more than a day.
	if prof2.Status.WindowSeconds < 24*60*60 {
		t.Errorf("WindowSeconds = %v, want >= 86400 (observedSince carried from profile)", prof2.Status.WindowSeconds)
	}
}

func TestParseObservedSince(t *testing.T) {
	fallback := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Empty -> fallback.
	if got := parseObservedSince("", fallback); !got.Equal(fallback) {
		t.Errorf("empty: got %v, want fallback %v", got, fallback)
	}
	// Invalid -> fallback.
	if got := parseObservedSince("not-a-time", fallback); !got.Equal(fallback) {
		t.Errorf("invalid: got %v, want fallback %v", got, fallback)
	}
	// Valid RFC3339 -> parsed.
	want := time.Date(2026, 5, 30, 8, 30, 0, 0, time.UTC)
	if got := parseObservedSince("2026-05-30T08:30:00Z", fallback); !got.Equal(want) {
		t.Errorf("valid: got %v, want %v", got, want)
	}
}
