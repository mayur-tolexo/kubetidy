// Package scan orchestrates a full scan: discover workloads, gather usage from the best
// available tier, price them, build recommendations, and compute the efficiency score.
package scan

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/kubetidy/kubetidy/internal/costmodel"
	"github.com/kubetidy/kubetidy/internal/model"
	"github.com/kubetidy/kubetidy/internal/pricing"
	"github.com/kubetidy/kubetidy/internal/rightsizer"
	"github.com/kubetidy/kubetidy/internal/score"
	"github.com/kubetidy/kubetidy/internal/usage"
)

// Engine wires the components together. It depends only on interfaces, so it is unit-testable
// with fakes.
type Engine struct {
	Workloads []model.Workload
	Usage     usage.Provider
	Price     pricing.Provider
	Policy    model.Policy
	Context   string
	Namespace string

	// Progress, when non-nil, is called once per workload as it is analyzed, with the number
	// completed so far and the total. It lets the CLI render a live progress indicator. It is
	// always optional; the engine never depends on it.
	Progress func(done, total int)
}

// Run executes the scan and returns a fully populated ScanResult.
//
// For each workload and container: fetch usage (skip/annotate when absent), compute the
// rightsizer proposal and confidence, price the delta, and assemble a Recommendation.
// Finally compute the efficiency score and aggregate total waste.
//
// Run degrades gracefully: a usage- or pricing-provider error for a single workload is
// recorded in result.Warnings and the scan continues, rather than failing the whole run.
func (e *Engine) Run(ctx context.Context) (model.ScanResult, error) {
	result := model.ScanResult{
		Context:       e.Context,
		Namespace:     e.Namespace,
		GeneratedAt:   time.Now(),
		WorkloadCount: len(e.Workloads),
	}
	if e.Usage != nil {
		result.Tier = e.Usage.Tier()
	}

	// Surface a prominent caveat when running on snapshot-only data, so nobody applies an
	// aggressive downsize from a single reading without understanding the risk.
	if result.Tier == model.TierSnapshot {
		result.Warnings = append(result.Warnings,
			"Tier 0 (metrics-server): recommendations are based on a single live snapshot, not historical peaks. "+
				"Treat them as directional and verify before applying. For safe, high-confidence numbers, point kubetidy "+
				"at Prometheus with --prometheus-url.")
	}

	for _, w := range e.Workloads {
		stats, err := e.Usage.Usage(ctx, w)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("usage unavailable for %s: %v", w.Ref(), err))
			continue
		}

		// Price the workload once. On error, warn and continue with a zero price so the
		// recommendations (sizing/confidence) are still produced, just without dollars.
		price, err := e.Price.ResourcePrice(ctx, w)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("pricing unavailable for %s: %v", w.Ref(), err))
			price = model.ResourcePrice{}
		}

		for _, c := range w.Containers {
			st, ok := stats[c.Name]
			if !ok {
				// MVP: no usage data for this container — skip it (nothing fatal).
				continue
			}

			current := model.ResourceSpec{Requests: c.Requests, Limits: c.Limits}
			proposed := rightsizer.Recommend(current, st, e.Policy)
			conf := rightsizer.Confidence(st)
			savings := costmodel.MonthlySavings(current, proposed, price, w.Replicas)

			// Noise filter: skip recommendations that change essentially nothing or that
			// save a trivial amount. A "512Mi→513Mi, $0/mo" row is just clutter.
			if isNoise(current, proposed, savings) {
				continue
			}

			rec := model.Recommendation{
				Workload:       w,
				ContainerName:  c.Name,
				Current:        current,
				Proposed:       proposed,
				MonthlySavings: savings,
				Confidence:     conf,
				Tier:           st.Tier,
				Evidence:       buildEvidence(st),
				Explanation:    buildExplanation(w, current, proposed, st, price, e.Policy, savings),
			}
			result.Recommendations = append(result.Recommendations, rec)

			if savings > 0 {
				result.TotalMonthlyWaste += savings
			}
		}
	}

	result.EfficiencyScore, result.ScoreBreakdown = score.Compute(result)
	return result, nil
}

// noiseFloorDollars is the minimum monthly saving for a recommendation to be worth showing.
const noiseFloorDollars = 1.0

// isNoise reports whether a recommendation is not worth surfacing: it saves less than the
// noise floor AND barely changes the requests (so it is neither a meaningful saving nor a
// meaningful safety change). Negative-saving "grow" recommendations are always kept — those
// are reliability findings, not noise.
func isNoise(current, proposed model.ResourceSpec, savings float64) bool {
	if savings <= -noiseFloorDollars {
		return false // a real "you should grow this" finding — keep it
	}
	if savings >= noiseFloorDollars {
		return false // a real saving — keep it
	}
	// Saving is within ±$1/mo: keep only if a request moved by a meaningful amount.
	const cpuEps = 25               // millicores
	const memEps = 32 * 1024 * 1024 // 32Mi
	cpuDelta := abs64(current.Requests.CPUMillicores - proposed.Requests.CPUMillicores)
	memDelta := abs64(current.Requests.MemoryBytes - proposed.Requests.MemoryBytes)
	return cpuDelta < cpuEps && memDelta < memEps
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// buildEvidence composes the short, human-readable justification line. It is tier-aware: a
// single live snapshot (Tier 0) is never dressed up as a percentile.
//   - Tier 0:  "live snapshot · cpu 1435m, mem 4.9Gi · 1 pod sampled"
//   - Tier 1+: "P95 cpu 280m, peak mem 0.9Gi over 14d · 1.2k samples"
func buildEvidence(st model.UsageStats) string {
	cpu := int64(math.Round(st.CPUMillicores.P95))
	if st.Tier == model.TierSnapshot {
		return fmt.Sprintf("live snapshot · cpu %s, mem %s · %s",
			formatMillicores(cpu), formatMem(st.MemoryBytes.Max), pods(st.Samples))
	}
	return fmt.Sprintf("P95 cpu %s, peak mem %s over %s · %s samples",
		formatMillicores(cpu), formatMem(st.MemoryBytes.Max),
		formatWindow(st.Window), formatCount(st.Samples))
}

// pods renders a pod-sample count ("1 pod sampled" / "9 pods sampled").
func pods(n int64) string {
	if n == 1 {
		return "1 pod sampled"
	}
	return fmt.Sprintf("%d pods sampled", n)
}

// formatCount renders large counts compactly (1200000 -> "1.2M", 9800 -> "9.8k").
func formatCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// formatMillicores renders CPU compactly: sub-core as "1435m", whole-ish cores as "2 cores".
func formatMillicores(m int64) string {
	if m >= 1000 && m%1000 == 0 {
		c := m / 1000
		if c == 1 {
			return "1 core"
		}
		return fmt.Sprintf("%d cores", c)
	}
	return fmt.Sprintf("%dm", m)
}

// formatMem renders a byte count with adaptive units and precision so small values are not
// flattened to "0.0Gi": Mi below 1Gi, Gi above, never losing the value to rounding.
func formatMem(bytes float64) string {
	const (
		mi = 1024 * 1024
		gi = 1024 * mi
	)
	switch {
	case bytes <= 0:
		return "0"
	case bytes < gi:
		return fmt.Sprintf("%dMi", int64(math.Round(bytes/mi)))
	default:
		return fmt.Sprintf("%.1fGi", bytes/gi)
	}
}

// buildExplanation composes the step-by-step derivation shown under --explain.
func buildExplanation(
	w model.Workload,
	current, proposed model.ResourceSpec,
	st model.UsageStats,
	price model.ResourcePrice,
	policy model.Policy,
	savings float64,
) []string {
	cpuHeadroom := policy.CPUHeadroom
	memHeadroom := policy.MemoryHeadroom
	var lines []string
	if st.Tier == model.TierSnapshot {
		cpuHeadroom += policy.SnapshotHeadroom
		memHeadroom += policy.SnapshotHeadroom
		lines = append(lines,
			fmt.Sprintf("Data: live snapshot from metrics-server (%s) — current usage only, not a peak.", pods(st.Samples)),
			"Because a single snapshot can miss peaks, an extra safety buffer is applied and values are floored.")
	} else {
		lines = append(lines,
			fmt.Sprintf("Data: tier %s over %s with %s samples.", st.Tier, formatWindow(st.Window), formatCount(st.Samples)))
	}
	lines = append(lines,
		fmt.Sprintf("CPU request = cpu %s * (1 + %.0f%% headroom) = %s (was %s).",
			formatMillicores(int64(math.Round(st.CPUMillicores.P95))),
			cpuHeadroom*100,
			formatMillicores(proposed.Requests.CPUMillicores),
			formatMillicores(current.Requests.CPUMillicores)),
		fmt.Sprintf("Memory request = peak %s * (1 + %.0f%% headroom) = %s (was %s).",
			formatMem(st.MemoryBytes.Max),
			memHeadroom*100,
			formatMem(float64(proposed.Requests.MemoryBytes)),
			formatMem(float64(current.Requests.MemoryBytes))))

	if price.Source != "" {
		lines = append(lines, fmt.Sprintf("Price: $%.2f/core-month, $%.2f/GiB-month (%s).",
			price.CPUCoreMonth, price.MemGiBMonth, price.Source))
	} else {
		lines = append(lines, fmt.Sprintf("Price: $%.2f/core-month, $%.2f/GiB-month.",
			price.CPUCoreMonth, price.MemGiBMonth))
	}

	replicas := w.Replicas
	if replicas <= 0 {
		replicas = 1
	}
	lines = append(lines, fmt.Sprintf(
		"Savings = (current - proposed cost) * %d replicas = $%.2f/month.",
		replicas, savings))

	return lines
}

// formatWindow renders a duration as a compact window (e.g. "14d", "6h", "0").
func formatWindow(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	days := d.Hours() / 24
	if days >= 1 {
		return fmt.Sprintf("%dd", int64(math.Round(days)))
	}
	return fmt.Sprintf("%dh", int64(math.Round(d.Hours())))
}
