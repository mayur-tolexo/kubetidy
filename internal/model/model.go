// Package model holds kubetidy's core domain types. It has no dependencies on other
// kubetidy packages and no I/O, so every other package can import it freely.
package model

import "time"

// EvidenceTier identifies which data source proved a finding. Higher tiers carry more
// confidence. See the design spec's "three-tier data ladder".
type EvidenceTier int

const (
	// TierStatic means no usage data was available; findings come from static analysis
	// of the spec (e.g. missing requests/limits, absurd request:limit ratios).
	TierStatic EvidenceTier = iota
	// TierSnapshot is a single live usage snapshot from metrics-server. It is only a degraded
	// fallback (used when the kubetidy operator is not installed): a single sample cannot see
	// peaks, so recommendations from it are conservative and low-confidence.
	TierSnapshot
	// TierOperator (Tier 0) is the primary "no Prometheus required" tier: historical
	// percentiles accumulated by the in-cluster kubetidy operator from metrics-server over
	// time — Prometheus-grade signal with zero external dependencies. See
	// docs/design/operator.md.
	TierOperator
	// TierHistorical (Tier 1) means historical percentiles from Prometheus.
	TierHistorical
	// TierAllocated (Tier 2) means precise allocated cost from OpenCost.
	TierAllocated
)

// String renders the tier for display.
func (t EvidenceTier) String() string {
	switch t {
	case TierStatic:
		return "static (no usage data)"
	case TierSnapshot:
		return "snapshot (metrics-server, limited)"
	case TierOperator:
		return "0 (kubetidy operator)"
	case TierHistorical:
		return "1 (Prometheus)"
	case TierAllocated:
		return "2 (OpenCost)"
	default:
		return "unknown"
	}
}

// WorkloadKind is the controller kind kubetidy analyzes.
type WorkloadKind string

// Workload kinds that kubetidy analyzes.
const (
	KindDeployment  WorkloadKind = "Deployment"
	KindStatefulSet WorkloadKind = "StatefulSet"
	KindDaemonSet   WorkloadKind = "DaemonSet"
)

// Workload is a controller (Deployment/StatefulSet/DaemonSet) discovered in the cluster,
// together with the per-container resource requests/limits from its pod template.
type Workload struct {
	Kind       WorkloadKind
	Name       string
	Namespace  string
	Replicas   int32
	Containers []Container
	// Selector is the label selector that matches this workload's pods, used by usage
	// providers to attribute pod metrics back to the workload.
	Selector map[string]string
	// NodeLabels (optional) captures scheduling hints (e.g. instance-type) for pricing.
	NodeLabels map[string]string
}

// Ref returns a stable "kind/namespace/name" identifier.
func (w Workload) Ref() string {
	return string(w.Kind) + "/" + w.Namespace + "/" + w.Name
}

// Container is a single container in a workload's pod template, with its current
// requests/limits.
type Container struct {
	Name     string
	Requests ResourceAmounts
	Limits   ResourceAmounts
}

// ResourceAmounts holds CPU and memory quantities in normalized units:
// CPU in millicores (1000m = 1 core), memory in bytes. Zero means "unset".
type ResourceAmounts struct {
	CPUMillicores int64
	MemoryBytes   int64
}

// IsZero reports whether both CPU and memory are unset.
func (r ResourceAmounts) IsZero() bool { return r.CPUMillicores == 0 && r.MemoryBytes == 0 }

// UsageStats holds observed usage for one container over an observation window.
// For Tier 0 (snapshot) P50/P95/Max may all equal the single sampled value and Samples==1.
type UsageStats struct {
	CPUMillicores Percentiles
	MemoryBytes   Percentiles
	Window        time.Duration
	Samples       int64
	Tier          EvidenceTier
}

// Percentiles holds summary statistics for a single metric over the window.
type Percentiles struct {
	P50 float64
	P95 float64
	Max float64
	// CV is the coefficient of variation (stddev/mean), used by the confidence model.
	// Zero when unknown.
	CV float64
}

// Policy controls how the rightsizer turns usage into recommended resources.
type Policy struct {
	// CPUHeadroom and MemoryHeadroom are fractional buffers added on top of the chosen
	// percentile (e.g. 0.15 = +15%).
	CPUHeadroom    float64
	MemoryHeadroom float64
	// SetCPULimit, when false (default), omits CPU limits to avoid throttling.
	SetCPULimit bool
	// MemoryLimitEqualsRequest, when true (default), sets memory limit == request
	// (Guaranteed QoS).
	MemoryLimitEqualsRequest bool

	// SnapshotHeadroom is the EXTRA fractional headroom applied on top of CPUHeadroom/
	// MemoryHeadroom when usage comes from a single live snapshot (Tier 0, metrics-server).
	// A snapshot captures current usage, never the peak, so downsizing from it is risky;
	// this large buffer keeps Tier-0 recommendations safe and conservative.
	SnapshotHeadroom float64

	// MinCPURequestMillicores and MinMemoryRequestBytes are floors: the rightsizer never
	// proposes a request below these, so an idle workload sampled at ~0 is not cut to an
	// unschedulable/throttled sliver.
	MinCPURequestMillicores int64
	MinMemoryRequestBytes   int64

	// DownsizeOnlyOnSnapshot, when true, means a snapshot-tier recommendation is only
	// emitted when it REDUCES a request (we trust "you asked for way more than you use"
	// from one sample, but not "you should grow" — that needs historical peaks).
	DownsizeOnlyOnSnapshot bool
}

// DefaultPolicy returns kubetidy's opinionated defaults (see design spec §6).
func DefaultPolicy() Policy {
	return Policy{
		CPUHeadroom:              0.15,
		MemoryHeadroom:           0.15,
		SetCPULimit:              false,
		MemoryLimitEqualsRequest: true,
		// Tier-0 safety: a single snapshot gets a big extra buffer, and floors keep
		// idle-at-scan-time workloads from being cut to nothing.
		SnapshotHeadroom:        1.0, // +100% on top of the base headroom for snapshots
		MinCPURequestMillicores: 10,
		MinMemoryRequestBytes:   32 * 1024 * 1024, // 32Mi
		DownsizeOnlyOnSnapshot:  true,
	}
}

// ResourcePrice is the unit price attributable to a workload's scheduling target.
type ResourcePrice struct {
	CPUCoreMonth float64 // $ per CPU core per month
	MemGiBMonth  float64 // $ per GiB of memory per month
	Source       string  // human-readable provenance (e.g. "node pricing", "OpenCost")
}

// Confidence is a 0..1 score with a human-readable rationale. It is derived and
// reproducible (see design spec §7), never cosmetic.
type Confidence struct {
	Score  float64 // 0..1
	Reason string
}

// Percent returns the confidence as an integer percentage 0..100.
func (c Confidence) Percent() int { return int(c.Score*100 + 0.5) }

// ConfidenceBand is a coarse, human-facing bucket for a confidence score. A precise percentage
// implies false precision ("85%" from two samples is misleading), so the UI leads with the band
// and keeps the number for detail views.
type ConfidenceBand string

// The three confidence buckets, from least to most trustworthy.
const (
	ConfidenceLow    ConfidenceBand = "low"
	ConfidenceMedium ConfidenceBand = "med"
	ConfidenceHigh   ConfidenceBand = "high"
)

// Band buckets the score: < 0.60 low, < 0.80 medium, otherwise high. The low cutoff sits above
// the snapshot ceiling (0.60) so a single-snapshot recommendation never reads above "low".
func (c Confidence) Band() ConfidenceBand {
	switch {
	case c.Score < 0.60:
		return ConfidenceLow
	case c.Score < 0.80:
		return ConfidenceMedium
	default:
		return ConfidenceHigh
	}
}

// Recommendation is the central, action-ready unit kubetidy produces. The MVP only reads,
// but this carries enough to generate a patch later (target ref, container, current vs
// proposed), so the future action layer is a new consumer, not a rewrite.
type Recommendation struct {
	Workload      Workload
	ContainerName string

	Current  ResourceSpec
	Proposed ResourceSpec

	// MonthlySavings is positive when the proposal saves money, negative when it costs
	// more (e.g. under-provisioned workload that should grow).
	MonthlySavings float64
	Confidence     Confidence
	Tier           EvidenceTier
	// Evidence is a short human-readable justification (e.g. "P95 cpu 280m, max mem 0.9Gi
	// over 14d · 1.2M samples").
	Evidence string
	// Explanation holds the full derivation shown under --explain.
	Explanation []string
}

// ResourceSpec pairs requests and limits for a single container.
type ResourceSpec struct {
	Requests ResourceAmounts
	Limits   ResourceAmounts
}

// ScanResult is the complete output of a scan.
type ScanResult struct {
	Context         string
	Namespace       string // empty means all namespaces
	Tier            EvidenceTier
	GeneratedAt     time.Time
	WorkloadCount   int
	Recommendations []Recommendation

	// EfficiencyScore is 0..100 (higher is better), computed by the score package.
	EfficiencyScore int
	ScoreBreakdown  []ScoreFactor
	// TotalMonthlyWaste is the sum of positive MonthlySavings across recommendations.
	TotalMonthlyWaste float64

	// Warnings collects non-fatal degradations (e.g. "Prometheus unreachable, used
	// metrics-server").
	Warnings []string
}

// ScoreFactor is one explainable component of the efficiency score.
type ScoreFactor struct {
	Name        string
	Value       float64 // 0..1 contribution
	Description string
}
