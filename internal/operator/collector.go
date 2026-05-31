// Package operator implements the kubetidy usage historian: a read-only, in-cluster
// controller that periodically samples metrics-server, accumulates per-container usage into
// decaying histograms, and checkpoints the result into UsageProfile custom resources. The
// CLI's "operator" usage provider then reads those CRDs so scans get Prometheus-grade
// recommendations with no Prometheus. See docs/design/operator.md.
//
// The Collector here is deliberately I/O-light and dependency-injected: it takes interfaces
// for "list workloads", "sample usage", and "persist a profile" so its accumulate/checkpoint
// logic is unit-testable without a cluster.
package operator

import (
	"context"
	"fmt"
	"time"

	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
	"github.com/kubetidy/kubetidy/internal/histogram"
	"github.com/kubetidy/kubetidy/internal/model"
)

// Sample is one observed usage reading for a container at a point in time.
type Sample struct {
	CPUMillicores float64
	MemoryBytes   float64
}

// Sampler returns the current per-container usage for a workload (keyed by container name).
// It is the operator's input edge; in production it is backed by metrics-server.
type Sampler interface {
	Sample(ctx context.Context, w model.Workload) (map[string]Sample, error)
}

// Store persists and loads UsageProfile objects. In production it is backed by the dynamic
// client writing CRDs; in tests it is an in-memory fake.
type Store interface {
	Get(ctx context.Context, namespace, name string) (usageprofile.UsageProfile, bool, error)
	Save(ctx context.Context, profile usageprofile.UsageProfile) error
}

// WorkloadLister returns the workloads the operator should profile.
type WorkloadLister interface {
	List(ctx context.Context) ([]model.Workload, error)
}

// Clock returns the current time. Injected so tests can drive decay deterministically.
type Clock func() time.Time

// containerKey uniquely identifies a container within a workload.
type containerKey struct {
	profile   string // <kind>-<name>, the UsageProfile object name
	namespace string
	container string
}

// containerState holds the live, in-memory histograms for one container plus bookkeeping.
type containerState struct {
	cpu          *histogram.Histogram
	mem          *histogram.Histogram
	observed     int64
	observedSlot time.Time // first observation time, for the window calculation
}

// Collector accumulates usage into per-container histograms and checkpoints them to the Store.
// It is safe to run on a single goroutine (one Tick at a time); it holds no internal locks
// because the operator's run loop is single-threaded.
type Collector struct {
	lister  WorkloadLister
	sampler Sampler
	store   Store
	now     Clock

	// state is the live histogram set, keyed by container. It survives across ticks and is the
	// source of truth between checkpoints.
	state map[containerKey]*containerState
}

// NewCollector builds a Collector. now may be nil, in which case time.Now is used.
func NewCollector(lister WorkloadLister, sampler Sampler, store Store, now Clock) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{
		lister:  lister,
		sampler: sampler,
		store:   store,
		now:     now,
		state:   make(map[containerKey]*containerState),
	}
}

// profileName returns the UsageProfile object name for a workload: "<kind>-<name>", lowercased
// by convention elsewhere; here we keep the workload's own casing for the lookup key and rely
// on the store to normalise if needed.
func profileName(w model.Workload) string {
	return fmt.Sprintf("%s-%s", w.Kind, w.Name)
}

// Tick performs one collection cycle: list workloads, sample each, fold the samples into the
// live histograms, and checkpoint every touched workload's UsageProfile. A per-workload error
// is collected and reported but never aborts the whole tick — the operator must be resilient.
func (c *Collector) Tick(ctx context.Context) error {
	workloads, err := c.lister.List(ctx)
	if err != nil {
		return fmt.Errorf("operator: listing workloads: %w", err)
	}

	var firstErr error
	for _, w := range workloads {
		if err := c.observeAndCheckpoint(ctx, w); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// observeAndCheckpoint samples one workload, updates its histograms, and saves its profile.
func (c *Collector) observeAndCheckpoint(ctx context.Context, w model.Workload) error {
	samples, err := c.sampler.Sample(ctx, w)
	if err != nil {
		return fmt.Errorf("operator: sampling %s: %w", w.Ref(), err)
	}
	if len(samples) == 0 {
		return nil // nothing to record this tick
	}

	now := c.now()
	name := profileName(w)
	for container, s := range samples {
		st := c.stateFor(containerKey{profile: name, namespace: w.Namespace, container: container}, now)
		if s.CPUMillicores > 0 {
			st.cpu.Observe(s.CPUMillicores, now)
		}
		if s.MemoryBytes > 0 {
			st.mem.Observe(s.MemoryBytes, now)
		}
		st.observed++
	}

	profile := c.buildProfile(w, name, now)
	if err := c.store.Save(ctx, profile); err != nil {
		return fmt.Errorf("operator: saving profile %s/%s: %w", w.Namespace, name, err)
	}
	return nil
}

// Rehydrate seeds the in-memory histograms from the persisted UsageProfile of each given
// workload, so an operator restart resumes exact percentile tracking instead of cold-starting.
// It is best-effort: a missing or malformed profile leaves that workload to start fresh, and a
// store error for one workload does not abort the rest.
func (c *Collector) Rehydrate(ctx context.Context, workloads []model.Workload) {
	for _, w := range workloads {
		name := profileName(w)
		profile, ok, err := c.store.Get(ctx, w.Namespace, name)
		if err != nil || !ok {
			continue
		}
		for _, ch := range profile.Status.Containers {
			c.state[containerKey{profile: name, namespace: w.Namespace, container: ch.Name}] = &containerState{
				cpu:          histogramFromMetric(ch.CPU, histogram.DefaultCPUConfig()),
				mem:          histogramFromMetric(ch.Memory, histogram.DefaultMemoryConfig()),
				observed:     profile.Status.SampleCount,
				observedSlot: parseObservedSince(profile.Status.ObservedSince, c.now()),
			}
		}
	}
}

// histogramFromMetric rebuilds a histogram from a persisted MetricHistory, falling back to a
// fresh histogram (with the given config) when the snapshot is absent or corrupt.
func histogramFromMetric(m usageprofile.MetricHistory, fallback histogram.Config) *histogram.Histogram {
	if snap, ok := decodeSnapshot(m.Histogram); ok {
		return histogram.FromSnapshot(snap, fallback)
	}
	return histogram.New(fallback)
}

// parseObservedSince parses an RFC3339 timestamp, returning fallback on any error.
func parseObservedSince(s string, fallback time.Time) time.Time {
	if s == "" {
		return fallback
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fallback
	}
	return t
}

// stateFor returns the live state for a container, lazily creating fresh histograms. Persisted
// state is seeded separately at startup via Rehydrate.
func (c *Collector) stateFor(key containerKey, now time.Time) *containerState {
	st, ok := c.state[key]
	if !ok {
		st = &containerState{
			cpu:          histogram.New(histogram.DefaultCPUConfig()),
			mem:          histogram.New(histogram.DefaultMemoryConfig()),
			observedSlot: now,
		}
		c.state[key] = st
	}
	return st
}

// buildProfile snapshots the current histograms for a workload into a UsageProfile ready to
// persist.
func (c *Collector) buildProfile(w model.Workload, name string, now time.Time) usageprofile.UsageProfile {
	profile := usageprofile.UsageProfile{
		Name:      name,
		Namespace: w.Namespace,
		Spec:      usageprofile.Spec{TargetRef: usageprofile.TargetRef{Kind: string(w.Kind), Name: w.Name}},
		Status: usageprofile.Status{
			LastUpdated: now.UTC().Format(time.RFC3339),
		},
	}

	var (
		totalSamples int64
		earliest     time.Time
	)
	for _, container := range w.Containers {
		key := containerKey{profile: name, namespace: w.Namespace, container: container.Name}
		st, ok := c.state[key]
		if !ok {
			continue
		}
		totalSamples += st.observed
		if earliest.IsZero() || st.observedSlot.Before(earliest) {
			earliest = st.observedSlot
		}
		profile.Status.Containers = append(profile.Status.Containers, usageprofile.ContainerHistory{
			Name:   container.Name,
			CPU:    metricHistory(st.cpu),
			Memory: metricHistory(st.mem),
		})
	}

	profile.Status.SampleCount = totalSamples
	if !earliest.IsZero() {
		profile.Status.ObservedSince = earliest.UTC().Format(time.RFC3339)
		profile.Status.WindowSeconds = now.Sub(earliest).Seconds()
	}
	return profile
}

// metricHistory snapshots one histogram into the persisted MetricHistory form, including the
// encoded snapshot for exact rehydration.
func metricHistory(h *histogram.Histogram) usageprofile.MetricHistory {
	return usageprofile.MetricHistory{
		P50:       h.Percentile(0.50),
		P95:       h.Percentile(0.95),
		Max:       h.Max(),
		Histogram: encodeSnapshot(h.ToSnapshot()),
	}
}
