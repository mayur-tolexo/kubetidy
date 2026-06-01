// Package rightsizer turns observed usage into recommended resources. It is a PURE package:
// no I/O, fully deterministic, exhaustively table-tested.
package rightsizer

import (
	"fmt"
	"math"
	"time"

	"github.com/kubetidy/kubetidy/internal/model"
)

// mib is one mebibyte in bytes; memory recommendations are rounded up to a whole MiB for
// cleaner, more human-readable values (e.g. 1.1Gi rather than 1181234567 bytes).
const mib = 1024 * 1024

// Recommend computes the proposed ResourceSpec for a container given its current spec, its
// observed usage, and the policy. It is the heart of kubetidy and must be deterministic.
//
// Defaults (see design spec §6):
//   - CPU request    = P95 * (1 + CPUHeadroom); no CPU limit unless policy.SetCPULimit.
//   - Memory request = Max * (1 + MemoryHeadroom); memory limit = request when
//     policy.MemoryLimitEqualsRequest, else carried over from current.
//
// When usage has no data for a metric (P95/Max == 0) the current request/limit for that
// metric is preserved (we never recommend zero from missing data). Memory is rounded up to
// the nearest whole MiB; CPU is rounded to whole millicores. No value can go negative.
//
// # Snapshot safety (Tier 0 / metrics-server)
//
// A single point-in-time sample (model.TierSnapshot, Samples==1) is a weak, dangerous
// signal: an idle workload momentarily using 1m of CPU or 5Mi of memory does NOT mean it
// can safely run with a 1m / 5Mi request — under load it would throttle or OOM. A real run
// produced terrifying advice like "redis-master mem 512Mi -> 7Mi" and "console cpu
// 2000m -> 1m" precisely because we trusted one snapshot. To keep snapshot-driven advice
// from being dangerous we apply three extra protections, all gated on
// usage.Tier == model.TierSnapshot:
//
//  1. Extra headroom: policy.SnapshotHeadroom is added on top of the base headroom for both
//     CPU and memory, so a snapshot reserves a much wider safety margin than a historical
//     percentile would (with defaults this is +115% instead of +15%).
//  2. Floors: the proposed request is never dropped below policy.MinCPURequestMillicores /
//     policy.MinMemoryRequestBytes. Each floor is disabled when its policy value is 0, and
//     a floor is only applied when there is real usage for that metric (we never invent a
//     request where current was 0 and usage is 0 — the zero-usage fallback that preserves
//     current still wins there). This stops "512Mi -> 5Mi" style cuts.
//  3. Downsize-only: when policy.DownsizeOnlyOnSnapshot is set we trust "you over-asked"
//     (so we clamp a request down toward current) but never "you should grow" — a single
//     idle sample must not push a request above its current value.
//
// Non-snapshot tiers keep all of the original behavior, including the ability to grow a
// request above current; only the floors apply to them (when configured).
func Recommend(current model.ResourceSpec, usage model.UsageStats, policy model.Policy) model.ResourceSpec {
	out := model.ResourceSpec{}

	snapshot := usage.Tier == model.TierSnapshot

	// Effective headroom: snapshots get the base headroom PLUS the extra snapshot headroom
	// to compensate for the weak single-sample signal. All other tiers use the base.
	cpuHeadroom := policy.CPUHeadroom
	memHeadroom := policy.MemoryHeadroom
	if snapshot {
		cpuHeadroom += policy.SnapshotHeadroom
		memHeadroom += policy.SnapshotHeadroom
	}

	// --- CPU ---
	if usage.CPUMillicores.P95 > 0 {
		cpu := roundNonNegative(usage.CPUMillicores.P95 * (1 + cpuHeadroom))

		// Floor: never recommend below the configured minimum (0 disables it). Applied only
		// because we have real usage for this metric.
		if policy.MinCPURequestMillicores > 0 && cpu < policy.MinCPURequestMillicores {
			cpu = policy.MinCPURequestMillicores
		}

		// Downsize-only on snapshot: a single sample must never justify growth.
		if snapshot && policy.DownsizeOnlyOnSnapshot &&
			current.Requests.CPUMillicores > 0 && cpu > current.Requests.CPUMillicores {
			cpu = current.Requests.CPUMillicores
		}

		out.Requests.CPUMillicores = cpu
		if policy.SetCPULimit {
			out.Limits.CPUMillicores = cpu
		}
		// else: leave CPU limit unset (0) to avoid throttling.
	} else {
		// No usage data: keep current CPU request/limit untouched.
		out.Requests.CPUMillicores = current.Requests.CPUMillicores
		out.Limits.CPUMillicores = current.Limits.CPUMillicores
	}

	// --- Memory ---
	if usage.MemoryBytes.Max > 0 {
		mem := roundUpMiB(roundNonNegative(usage.MemoryBytes.Max * (1 + memHeadroom)))

		// Floor: never recommend below the configured minimum (0 disables it). Applied only
		// because we have real usage for this metric. Round the floor up to a whole MiB so
		// the proposal stays MiB-aligned.
		if policy.MinMemoryRequestBytes > 0 && mem < roundUpMiB(policy.MinMemoryRequestBytes) {
			mem = roundUpMiB(policy.MinMemoryRequestBytes)
		}

		// Downsize-only on snapshot: a single sample must never justify growth.
		if snapshot && policy.DownsizeOnlyOnSnapshot &&
			current.Requests.MemoryBytes > 0 && mem > current.Requests.MemoryBytes {
			mem = current.Requests.MemoryBytes
		}

		out.Requests.MemoryBytes = mem
		if policy.MemoryLimitEqualsRequest {
			out.Limits.MemoryBytes = mem
		} else {
			out.Limits.MemoryBytes = current.Limits.MemoryBytes
		}
	} else {
		// No usage data: keep current memory request/limit untouched.
		out.Requests.MemoryBytes = current.Requests.MemoryBytes
		out.Limits.MemoryBytes = current.Limits.MemoryBytes
	}

	return out
}

// roundNonNegative rounds to the nearest whole unit, clamping NaN/Inf and negatives to 0.
func roundNonNegative(v float64) int64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0
	}
	return int64(math.Round(v))
}

// roundUpMiB rounds a byte count up to the nearest whole mebibyte.
func roundUpMiB(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	mibs := (bytes + mib - 1) / mib
	return mibs * mib
}

// Confidence derives a reproducible confidence score from the usage statistics (tier,
// window, sample count, variance). See design spec §7.
//
// The score starts from a per-tier base, gains a bonus for long windows (>= 14d) and large
// sample counts, and loses a penalty proportional to the metrics' coefficient of variation.
// TierSnapshot is capped at 0.6 (a single live snapshot is never high-confidence). The final
// score is clamped to [0.05, 0.99].
// Data-maturity references: a time-series tier earns its full base confidence only once it has
// accumulated roughly this much window AND this many samples. Below that it is "still warming
// up" and confidence scales down toward immatureFloor — so a freshly-installed operator with two
// readings reads "low", not "high". Picked so a Tier-0 operator (30s scrape) reaches full
// maturity after ~3 days; Prometheus with a 14d window matures quickly on sample count.
const (
	matureWindowHours = 72.0
	matureSamples     = 8000.0
	immatureFloor     = 0.30
)

// Confidence scores a recommendation 0..1 from its usage evidence. Snapshot/static tiers use a
// fixed (low) base; time-series tiers (operator, Prometheus, OpenCost) earn their high base only
// as data matures (enough window AND samples), then lose ground to observed variance.
func Confidence(usage model.UsageStats) model.Confidence {
	base := tierBase(usage.Tier)

	// Variance penalty: driven by the worst-behaved (highest CV) of CPU and memory.
	// A CV of 1.0 (stddev == mean) costs the full 0.3.
	worstCV := math.Max(usage.CPUMillicores.CV, usage.MemoryBytes.CV)
	if worstCV < 0 {
		worstCV = 0
	}
	variancePenalty := 0.3 * clamp01(worstCV)

	var score float64
	if usage.Tier == model.TierSnapshot || usage.Tier == model.TierStatic {
		// A single snapshot / spec-only check does not accumulate over time, so maturity does
		// not apply; the tier base already reflects its inherent (low) confidence.
		score = base - variancePenalty
		if usage.Tier == model.TierSnapshot && score > 0.6 {
			score = 0.6 // a single snapshot can never be high-confidence
		}
	} else {
		// Time-series tiers (operator, Prometheus, OpenCost): gate the tier's high base on how
		// much real history backs it. maturity is the weakest of window- and sample-coverage,
		// so BOTH enough elapsed time and enough samples are required to earn full confidence.
		windowMaturity := clamp01(usage.Window.Hours() / matureWindowHours)
		sampleMaturity := 0.0
		if usage.Samples > 1 {
			sampleMaturity = clamp01(math.Log10(float64(usage.Samples)) / math.Log10(matureSamples))
		}
		maturity := math.Min(windowMaturity, sampleMaturity)
		score = immatureFloor + maturity*(base-immatureFloor) - variancePenalty
	}

	score = clamp(score, 0.05, 0.99)

	reason := buildReason(usage, worstCV)
	return model.Confidence{Score: score, Reason: reason}
}

// tierBase returns the starting confidence for a tier.
func tierBase(t model.EvidenceTier) float64 {
	switch t {
	case model.TierHistorical:
		return 0.9
	case model.TierAllocated:
		return 0.9
	case model.TierOperator:
		// Operator history (Tier 0) is real percentiles over time (not a single sample), so it
		// earns near-Prometheus confidence — slightly below Tier 1 since it is derived from
		// metrics-server's coarser cadence rather than Prometheus' fine-grained scrapes.
		return 0.85
	case model.TierSnapshot:
		return 0.5
	case model.TierStatic:
		return 0.25
	default:
		return 0.25
	}
}

// buildReason produces a short human-readable explanation of the confidence drivers.
func buildReason(usage model.UsageStats, worstCV float64) string {
	var variance string
	switch {
	case worstCV == 0:
		variance = "no variance data"
	case worstCV < 0.25:
		variance = "low variance"
	case worstCV < 0.75:
		variance = "moderate variance"
	default:
		variance = "high variance"
	}
	return fmt.Sprintf("tier %s; window %s; %d samples; %s (CV %.2f)",
		usage.Tier, formatWindow(usage.Window), usage.Samples, variance, worstCV)
}

// formatWindow renders a duration as a compact human window (e.g. "14d", "6h", "0").
func formatWindow(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	if days := d.Hours() / 24; days >= 1 {
		return fmt.Sprintf("%dd", int64(math.Round(days)))
	}
	if d.Hours() >= 1 {
		return fmt.Sprintf("%dh", int64(math.Round(d.Hours())))
	}
	if d.Minutes() >= 1 {
		return fmt.Sprintf("%dm", int64(math.Round(d.Minutes())))
	}
	return "<1m"
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clamp01(v float64) float64 { return clamp(v, 0, 1) }
