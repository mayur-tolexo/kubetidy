// Package histogram implements a compact, exponentially-decaying bucketed histogram for
// recording a single metric (CPU millicores or memory bytes) observed over time.
//
// It is the storage core of the kubetidy operator (see docs/design/operator.md). The design
// follows the Vertical Pod Autoscaler's recommender: instead of keeping a raw time series
// (which would reinvent Prometheus and overwhelm etcd), each observation adds a time-decaying
// weight to a fixed set of exponential buckets. This gives:
//
//   - fixed memory per metric (a small, constant-size bucket array),
//   - O(1) updates,
//   - O(buckets) percentile reads (P50/P95/max queryable directly),
//   - recency bias via an exponential half-life, so recent behaviour dominates while a weekly
//     spike still registers within the retention window.
//
// The package is PURE: it has no I/O and no dependency on other kubetidy packages, so it is
// exhaustively unit-testable.
package histogram

import (
	"math"
	"time"
)

// Config parameterises a Histogram's bucket layout and decay.
type Config struct {
	// FirstBucketUpper is the upper bound (in the metric's native unit) of bucket 0. Values at
	// or below it fall in the first bucket. Must be > 0.
	FirstBucketUpper float64
	// Ratio is the geometric growth factor between consecutive bucket upper bounds. Must be > 1.
	// e.g. with FirstBucketUpper=10 and Ratio=1.2, bucket bounds are 10, 12, 14.4, ...
	Ratio float64
	// NumBuckets is the number of buckets. Values above the last bucket's bound are clamped
	// into the last bucket. Must be >= 1.
	NumBuckets int
	// HalfLife is the duration over which a sample's weight decays by half. Must be > 0.
	HalfLife time.Duration
}

// DefaultCPUConfig returns a bucket layout suited to CPU usage in millicores: from ~1m up to
// roughly 64 cores across exponential buckets, with a 24h half-life.
func DefaultCPUConfig() Config {
	return Config{FirstBucketUpper: 1, Ratio: 1.2, NumBuckets: 64, HalfLife: 24 * time.Hour}
}

// DefaultMemoryConfig returns a bucket layout suited to memory usage in bytes: from ~1Mi up to
// roughly 128Gi across exponential buckets, with a 24h half-life.
func DefaultMemoryConfig() Config {
	return Config{FirstBucketUpper: 1024 * 1024, Ratio: 1.25, NumBuckets: 64, HalfLife: 24 * time.Hour}
}

// Histogram is an exponentially-decaying bucketed histogram. The zero value is not usable;
// construct one with New.
//
// Decay is implemented as a reference-time technique: rather than rescaling every bucket on
// each update (O(buckets) per sample), each added weight is scaled UP relative to a fixed
// reference time. Percentiles are scale-invariant, so the ever-growing weights never need to
// be normalised for reads. The reference time advances lazily to bound the exponent and avoid
// floating-point overflow over long uptimes.
type Histogram struct {
	cfg     Config
	bounds  []float64 // upper bound of each bucket, precomputed
	weights []float64 // accumulated decayed weight per bucket
	refTime time.Time // reference time the weights are expressed relative to
	maxSeen float64   // largest raw value ever observed (for an exact max read)
	total   float64   // sum of current weights (kept in sync for fast percentile math)
}

// New builds a Histogram from cfg. Invalid configs are coerced to safe defaults so callers
// never get a nil or panicking histogram.
func New(cfg Config) *Histogram {
	if cfg.FirstBucketUpper <= 0 {
		cfg.FirstBucketUpper = 1
	}
	if cfg.Ratio <= 1 {
		cfg.Ratio = 1.2
	}
	if cfg.NumBuckets < 1 {
		cfg.NumBuckets = 1
	}
	if cfg.HalfLife <= 0 {
		cfg.HalfLife = 24 * time.Hour
	}

	bounds := make([]float64, cfg.NumBuckets)
	bound := cfg.FirstBucketUpper
	for i := 0; i < cfg.NumBuckets; i++ {
		bounds[i] = bound
		bound *= cfg.Ratio
	}
	return &Histogram{
		cfg:     cfg,
		bounds:  bounds,
		weights: make([]float64, cfg.NumBuckets),
	}
}

// bucketFor returns the index of the bucket that value falls into. Values above the last bound
// are clamped to the final bucket; values <= 0 go to bucket 0.
func (h *Histogram) bucketFor(value float64) int {
	for i, b := range h.bounds {
		if value <= b {
			return i
		}
	}
	return len(h.bounds) - 1
}

// decayFactor returns 2^(elapsed/halfLife), the factor by which a weight added at t is scaled
// (relative to the reference time) so that older samples effectively weigh less.
func (h *Histogram) decayFactor(t time.Time) float64 {
	elapsed := t.Sub(h.refTime).Seconds()
	return math.Exp2(elapsed / h.cfg.HalfLife.Seconds())
}

// Observe records value (in the metric's native unit) seen at time t with unit weight.
func (h *Histogram) Observe(value float64, t time.Time) {
	h.ObserveWeighted(value, 1, t)
}

// ObserveWeighted records value seen at time t with the given sample weight.
func (h *Histogram) ObserveWeighted(value, weight float64, t time.Time) {
	if weight <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	if h.refTime.IsZero() {
		h.refTime = t
	}
	// Keep the decay exponent bounded: once samples are far ahead of the reference time,
	// re-base the reference forward and rescale existing weights down accordingly.
	h.maybeRebase(t)

	scaled := weight * h.decayFactor(t)
	idx := h.bucketFor(value)
	h.weights[idx] += scaled
	h.total += scaled
	if value > h.maxSeen {
		h.maxSeen = value
	}
}

// rebaseThreshold is how far (in half-lives) the newest sample may run ahead of the reference
// time before we re-base, to keep 2^x from overflowing float64.
const rebaseThreshold = 16

// maybeRebase advances the reference time toward t when the decay exponent grows too large,
// rescaling all stored weights so percentiles are unchanged.
func (h *Histogram) maybeRebase(t time.Time) {
	halfLives := t.Sub(h.refTime).Seconds() / h.cfg.HalfLife.Seconds()
	if halfLives < rebaseThreshold {
		return
	}
	factor := math.Exp2(halfLives) // amount weights would have been scaled up by
	for i := range h.weights {
		h.weights[i] /= factor
	}
	h.total /= factor
	h.refTime = t
}

// Percentile returns the upper bound of the bucket at the given quantile (0..1) of the current
// decayed distribution. It returns 0 when the histogram is empty. Because percentiles are
// scale-invariant, the un-normalised weights can be used directly.
func (h *Histogram) Percentile(q float64) float64 {
	if h.total <= 0 {
		return 0
	}
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	target := q * h.total
	var cumulative float64
	for i, w := range h.weights {
		cumulative += w
		if cumulative >= target {
			return h.bounds[i]
		}
	}
	return h.bounds[len(h.bounds)-1]
}

// Max returns the largest raw value ever observed. Unlike Percentile it is not decayed: for
// memory in particular the historical peak is the safety-relevant figure (OOM risk), so we
// keep an exact maximum rather than a decayed estimate.
func (h *Histogram) Max() float64 { return h.maxSeen }

// IsEmpty reports whether the histogram has recorded any weight.
func (h *Histogram) IsEmpty() bool { return h.total <= 0 }

// Snapshot is a serialisable view of a Histogram's state, used to checkpoint into and rehydrate
// from the UsageProfile CRD.
type Snapshot struct {
	FirstBucketUpper float64   `json:"firstBucketUpper"`
	Ratio            float64   `json:"ratio"`
	NumBuckets       int       `json:"numBuckets"`
	HalfLifeSeconds  float64   `json:"halfLifeSeconds"`
	Weights          []float64 `json:"weights"`
	RefTimeUnixNano  int64     `json:"refTimeUnixNano"`
	MaxSeen          float64   `json:"maxSeen"`
}

// ToSnapshot returns a serialisable copy of the histogram's state.
func (h *Histogram) ToSnapshot() Snapshot {
	w := make([]float64, len(h.weights))
	copy(w, h.weights)
	var refNano int64
	if !h.refTime.IsZero() {
		refNano = h.refTime.UnixNano()
	}
	return Snapshot{
		FirstBucketUpper: h.cfg.FirstBucketUpper,
		Ratio:            h.cfg.Ratio,
		NumBuckets:       h.cfg.NumBuckets,
		HalfLifeSeconds:  h.cfg.HalfLife.Seconds(),
		Weights:          w,
		RefTimeUnixNano:  refNano,
		MaxSeen:          h.maxSeen,
	}
}

// FromSnapshot rebuilds a Histogram from a previously persisted Snapshot. A zero or malformed
// snapshot yields a fresh histogram using the provided fallback config, so a corrupt checkpoint
// degrades to "start over" rather than panicking.
func FromSnapshot(s Snapshot, fallback Config) *Histogram {
	cfg := Config{
		FirstBucketUpper: s.FirstBucketUpper,
		Ratio:            s.Ratio,
		NumBuckets:       s.NumBuckets,
		HalfLife:         time.Duration(s.HalfLifeSeconds * float64(time.Second)),
	}
	h := New(cfg)
	if len(s.Weights) == len(h.weights) {
		copy(h.weights, s.Weights)
		for _, w := range s.Weights {
			h.total += w
		}
	} else if len(s.Weights) != 0 {
		// Bucket layout changed (e.g. config upgrade): fall back to a clean histogram.
		h = New(fallback)
	}
	h.maxSeen = s.MaxSeen
	if s.RefTimeUnixNano != 0 {
		h.refTime = time.Unix(0, s.RefTimeUnixNano)
	}
	return h
}
