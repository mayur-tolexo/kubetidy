package histogram

import (
	"math"
	"testing"
	"time"
)

// base is a fixed reference instant so every test is deterministic.
var base = time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

func TestNewCoercesInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"zeroFirstBucket", Config{FirstBucketUpper: 0, Ratio: 1.2, NumBuckets: 8, HalfLife: time.Hour}},
		{"negativeFirstBucket", Config{FirstBucketUpper: -5, Ratio: 1.2, NumBuckets: 8, HalfLife: time.Hour}},
		{"ratioEqualOne", Config{FirstBucketUpper: 1, Ratio: 1, NumBuckets: 8, HalfLife: time.Hour}},
		{"ratioBelowOne", Config{FirstBucketUpper: 1, Ratio: 0.5, NumBuckets: 8, HalfLife: time.Hour}},
		{"zeroBuckets", Config{FirstBucketUpper: 1, Ratio: 1.2, NumBuckets: 0, HalfLife: time.Hour}},
		{"negativeBuckets", Config{FirstBucketUpper: 1, Ratio: 1.2, NumBuckets: -3, HalfLife: time.Hour}},
		{"zeroHalfLife", Config{FirstBucketUpper: 1, Ratio: 1.2, NumBuckets: 8, HalfLife: 0}},
		{"negativeHalfLife", Config{FirstBucketUpper: 1, Ratio: 1.2, NumBuckets: 8, HalfLife: -time.Hour}},
		{"allInvalid", Config{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var h *Histogram
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("New panicked on %s: %v", tc.name, r)
					}
				}()
				h = New(tc.cfg)
			}()
			if h == nil {
				t.Fatalf("New returned nil for %s", tc.name)
			}
			if h.cfg.FirstBucketUpper <= 0 {
				t.Errorf("FirstBucketUpper not coerced: %v", h.cfg.FirstBucketUpper)
			}
			if h.cfg.Ratio <= 1 {
				t.Errorf("Ratio not coerced: %v", h.cfg.Ratio)
			}
			if h.cfg.NumBuckets < 1 {
				t.Errorf("NumBuckets not coerced: %v", h.cfg.NumBuckets)
			}
			if h.cfg.HalfLife <= 0 {
				t.Errorf("HalfLife not coerced: %v", h.cfg.HalfLife)
			}
			if len(h.bounds) != h.cfg.NumBuckets || len(h.weights) != h.cfg.NumBuckets {
				t.Errorf("bucket arrays length mismatch: bounds=%d weights=%d num=%d",
					len(h.bounds), len(h.weights), h.cfg.NumBuckets)
			}
			// A coerced histogram must still be usable.
			h.Observe(1, base)
			if h.IsEmpty() {
				t.Errorf("histogram still empty after Observe for %s", tc.name)
			}
		})
	}
}

func TestEmptyHistogram(t *testing.T) {
	h := New(DefaultCPUConfig())
	if !h.IsEmpty() {
		t.Fatal("fresh histogram should be empty")
	}
	if got := h.Percentile(0.5); got != 0 {
		t.Errorf("Percentile on empty = %v, want 0", got)
	}
	if got := h.Percentile(0.95); got != 0 {
		t.Errorf("Percentile(0.95) on empty = %v, want 0", got)
	}
	if got := h.Max(); got != 0 {
		t.Errorf("Max on empty = %v, want 0", got)
	}
}

func TestPercentileSaneBounds(t *testing.T) {
	h := New(DefaultCPUConfig())
	// Observe many samples clustered around 100 millicores, all at the same instant.
	for i := 0; i < 1000; i++ {
		h.Observe(100, base)
	}
	// Add a smaller set of high-value samples so P95 > P50.
	for i := 0; i < 60; i++ {
		h.Observe(400, base)
	}
	if h.IsEmpty() {
		t.Fatal("histogram should not be empty after observing")
	}
	p50 := h.Percentile(0.5)
	p95 := h.Percentile(0.95)
	if p50 <= 0 || p95 <= 0 {
		t.Fatalf("percentiles should be positive: p50=%v p95=%v", p50, p95)
	}
	if p95 < p50 {
		t.Errorf("P95 (%v) should be >= P50 (%v)", p95, p50)
	}
	// P50 should land in a bucket near the bulk value of 100.
	if p50 < 50 || p50 > 200 {
		t.Errorf("P50 %v not in sane bucket range around 100", p50)
	}
	// P95 should reflect the high-value tail near 400.
	if p95 < 200 {
		t.Errorf("P95 %v should reflect the 400 tail", p95)
	}
}

func TestMaxIsRawAndNotDecayed(t *testing.T) {
	h := New(DefaultCPUConfig())
	// Big value observed long in the past.
	h.Observe(5000, base.Add(-10*24*time.Hour))
	// Many newer, smaller values.
	for i := 0; i < 500; i++ {
		h.Observe(50, base)
	}
	if got := h.Max(); got != 5000 {
		t.Errorf("Max = %v, want 5000 (largest raw value, not decayed)", got)
	}
	// A still-smaller later value must not lower Max.
	h.Observe(10, base.Add(time.Hour))
	if got := h.Max(); got != 5000 {
		t.Errorf("Max after smaller sample = %v, want 5000", got)
	}
}

func TestDecayFavorsRecentSamples(t *testing.T) {
	cfg := Config{FirstBucketUpper: 1, Ratio: 1.2, NumBuckets: 64, HalfLife: 24 * time.Hour}
	h := New(cfg)
	// One big value far in the past.
	t0 := base
	h.Observe(2000, t0)
	// Many small values many half-lives later.
	now := t0.Add(8 * 24 * time.Hour) // 8 half-lives later
	for i := 0; i < 200; i++ {
		h.Observe(50, now)
	}
	p95 := h.Percentile(0.95)
	if p95 <= 0 {
		t.Fatalf("P95 should be positive, got %v", p95)
	}
	// The old big sample has decayed by 2^-8 (~0.004) relative to each new one, and there are
	// 200 new samples, so the recent small values dominate the distribution.
	if p95 > 200 {
		t.Errorf("P95 %v should reflect recent small samples (~50), old spike should have decayed", p95)
	}
	// Max is still the raw historical peak though.
	if h.Max() != 2000 {
		t.Errorf("Max = %v, want 2000", h.Max())
	}
}

func TestRebaseSafetyManyHalfLives(t *testing.T) {
	cfg := Config{FirstBucketUpper: 1, Ratio: 1.2, NumBuckets: 64, HalfLife: 24 * time.Hour}
	h := New(cfg)
	start := base
	// Observe samples spanning well beyond the rebaseThreshold (16 half-lives) repeatedly,
	// so the rebase path executes multiple times.
	for i := 0; i < 50; i++ {
		ts := start.Add(time.Duration(i) * 20 * 24 * time.Hour) // 20 half-lives apart each step
		h.Observe(float64(100+i), ts)
	}
	p50 := h.Percentile(0.5)
	p95 := h.Percentile(0.95)
	mx := h.Max()
	for _, v := range []float64{p50, p95, mx} {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			t.Fatalf("non-finite percentile/max after long span: p50=%v p95=%v max=%v", p50, p95, mx)
		}
	}
	if p50 <= 0 || p95 <= 0 {
		t.Errorf("percentiles should be positive after rebase: p50=%v p95=%v", p50, p95)
	}
	if p95 < p50 {
		t.Errorf("P95 (%v) should be >= P50 (%v) after rebase", p95, p50)
	}
	// total weight must remain finite and positive.
	if math.IsInf(h.total, 0) || math.IsNaN(h.total) || h.total <= 0 {
		t.Errorf("total weight invalid after rebase: %v", h.total)
	}
}

func TestObserveWeightedIgnoresBadInput(t *testing.T) {
	h := New(DefaultCPUConfig())
	h.ObserveWeighted(100, 0, base)                 // zero weight ignored
	h.ObserveWeighted(100, -1, base)                // negative weight ignored
	h.ObserveWeighted(math.NaN(), 1, base)          // NaN value ignored
	h.ObserveWeighted(math.Inf(1), 1, base)         // +Inf value ignored
	h.ObserveWeighted(math.Inf(-1), 1, base)        // -Inf value ignored
	h.ObserveWeighted(math.NaN(), math.NaN(), base) // NaN value ignored
	if !h.IsEmpty() {
		t.Fatalf("histogram should still be empty after only bad inputs, total=%v", h.total)
	}
	// A valid weighted observation registers.
	h.ObserveWeighted(100, 2.5, base)
	if h.IsEmpty() {
		t.Error("histogram should record a valid weighted observation")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	h := New(DefaultMemoryConfig())
	for i := 0; i < 300; i++ {
		h.Observe(5e8, base)
	}
	for i := 0; i < 20; i++ {
		h.Observe(9e8, base.Add(time.Hour))
	}
	snap := h.ToSnapshot()

	got := FromSnapshot(snap, DefaultCPUConfig())

	if got.Percentile(0.5) != h.Percentile(0.5) {
		t.Errorf("P50 mismatch after round trip: got %v want %v", got.Percentile(0.5), h.Percentile(0.5))
	}
	if got.Percentile(0.95) != h.Percentile(0.95) {
		t.Errorf("P95 mismatch after round trip: got %v want %v", got.Percentile(0.95), h.Percentile(0.95))
	}
	if got.Max() != h.Max() {
		t.Errorf("Max mismatch after round trip: got %v want %v", got.Max(), h.Max())
	}
	if got.IsEmpty() != h.IsEmpty() {
		t.Errorf("IsEmpty mismatch after round trip: got %v want %v", got.IsEmpty(), h.IsEmpty())
	}

	// Further observations on the rehydrated histogram should continue consistently
	// (refTime preserved): the snapshot decay state survives.
	got.Observe(5e8, base.Add(2*time.Hour))
	if math.IsInf(got.Percentile(0.95), 0) || math.IsNaN(got.Percentile(0.95)) {
		t.Error("non-finite P95 after observing on rehydrated histogram")
	}
}

func TestFromSnapshotMismatchedWeightsFallsBack(t *testing.T) {
	fallback := DefaultCPUConfig()
	// A snapshot whose Weights length does not match its declared NumBuckets layout.
	snap := Snapshot{
		FirstBucketUpper: 1,
		Ratio:            1.2,
		NumBuckets:       64,
		HalfLifeSeconds:  (24 * time.Hour).Seconds(),
		Weights:          []float64{1, 2, 3}, // length 3 != 64
		MaxSeen:          999,
	}
	h := FromSnapshot(snap, fallback)
	// Falls back to a clean histogram with the fallback config.
	if h.cfg.NumBuckets != fallback.NumBuckets {
		t.Errorf("expected fallback NumBuckets %d, got %d", fallback.NumBuckets, h.cfg.NumBuckets)
	}
	if h.cfg.FirstBucketUpper != fallback.FirstBucketUpper {
		t.Errorf("expected fallback FirstBucketUpper %v, got %v", fallback.FirstBucketUpper, h.cfg.FirstBucketUpper)
	}
	// total weight should be zero (fresh) since mismatched weights were discarded.
	if h.total != 0 {
		t.Errorf("expected fresh (zero total) histogram on fallback, got total=%v", h.total)
	}
	// MaxSeen is still carried over from the snapshot.
	if h.Max() != 999 {
		t.Errorf("MaxSeen = %v, want 999", h.Max())
	}
}

func TestFromSnapshotEmptyYieldsEmpty(t *testing.T) {
	h := FromSnapshot(Snapshot{}, DefaultCPUConfig())
	if h == nil {
		t.Fatal("FromSnapshot returned nil for empty snapshot")
	}
	if !h.IsEmpty() {
		t.Errorf("empty snapshot should yield empty histogram, total=%v", h.total)
	}
	if h.Max() != 0 {
		t.Errorf("empty snapshot Max = %v, want 0", h.Max())
	}
	// Config was coerced to defaults (all zero in the empty snapshot).
	if h.cfg.FirstBucketUpper <= 0 || h.cfg.Ratio <= 1 || h.cfg.NumBuckets < 1 || h.cfg.HalfLife <= 0 {
		t.Errorf("empty snapshot config not coerced to safe defaults: %+v", h.cfg)
	}
	// Still usable.
	h.Observe(100, base)
	if h.IsEmpty() {
		t.Error("rehydrated-from-empty histogram should accept observations")
	}
}

func TestSnapshotPreservesRefTimeAndWeights(t *testing.T) {
	h := New(DefaultCPUConfig())
	h.Observe(100, base)
	snap := h.ToSnapshot()
	if snap.RefTimeUnixNano != base.UnixNano() {
		t.Errorf("snapshot RefTimeUnixNano = %d, want %d", snap.RefTimeUnixNano, base.UnixNano())
	}
	if len(snap.Weights) != h.cfg.NumBuckets {
		t.Errorf("snapshot weights len = %d, want %d", len(snap.Weights), h.cfg.NumBuckets)
	}
	if snap.NumBuckets != h.cfg.NumBuckets {
		t.Errorf("snapshot NumBuckets = %d, want %d", snap.NumBuckets, h.cfg.NumBuckets)
	}
	// A fresh (never-observed) histogram snapshots with a zero refTime.
	fresh := New(DefaultCPUConfig())
	fsnap := fresh.ToSnapshot()
	if fsnap.RefTimeUnixNano != 0 {
		t.Errorf("fresh snapshot RefTimeUnixNano = %d, want 0", fsnap.RefTimeUnixNano)
	}
}

func TestPercentileClampsQuantile(t *testing.T) {
	h := New(DefaultCPUConfig())
	for i := 0; i < 100; i++ {
		h.Observe(100, base)
	}
	// q < 0 clamps to 0, q > 1 clamps to 1; both must return finite positive bounds.
	lo := h.Percentile(-0.5)
	hi := h.Percentile(1.5)
	if lo <= 0 || hi <= 0 {
		t.Fatalf("clamped percentiles should be positive: lo=%v hi=%v", lo, hi)
	}
	if hi < lo {
		t.Errorf("Percentile(1.5) (%v) should be >= Percentile(-0.5) (%v)", hi, lo)
	}
}

func TestObserveClampsHighValueToLastBucket(t *testing.T) {
	cfg := Config{FirstBucketUpper: 1, Ratio: 2, NumBuckets: 4, HalfLife: time.Hour}
	h := New(cfg)
	// Last bucket bound is 1*2^3 = 8. Observe a value far above it.
	h.Observe(1_000_000, base)
	lastBound := h.bounds[len(h.bounds)-1]
	if got := h.Percentile(0.95); got != lastBound {
		t.Errorf("P95 of clamped-high value = %v, want last bound %v", got, lastBound)
	}
	if h.Max() != 1_000_000 {
		t.Errorf("Max should keep raw value: got %v", h.Max())
	}
}

func TestMean(t *testing.T) {
	// Empty histogram → 0.
	h := New(DefaultCPUConfig())
	if h.Mean() != 0 {
		t.Errorf("empty Mean = %v, want 0", h.Mean())
	}

	// Observations clustered low → Mean sits between the low and high values, and below the max.
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 100; i++ {
		h.Observe(10, base) // 100 samples at ~10m
	}
	h.Observe(1000, base) // one spike
	mean := h.Mean()
	if mean <= 0 {
		t.Fatalf("Mean = %v, want > 0", mean)
	}
	if mean >= h.Max() {
		t.Errorf("Mean %v should be below Max %v", mean, h.Max())
	}
	// Dominated by the 10m cluster, so the mean should be far closer to 10 than to 1000.
	if mean > 200 {
		t.Errorf("Mean = %v, want close to the low cluster (~10), not the spike", mean)
	}
}
