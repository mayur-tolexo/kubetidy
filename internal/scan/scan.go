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

// buildEvidence composes the short, human-readable justification line, e.g.
// "P95 cpu 280m, max mem 0.9Gi over 14d · 1200000 samples".
func buildEvidence(st model.UsageStats) string {
	return fmt.Sprintf("P95 cpu %dm, max mem %s over %s · %d samples",
		int64(math.Round(st.CPUMillicores.P95)),
		formatGi(st.MemoryBytes.Max),
		formatWindow(st.Window),
		st.Samples)
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
	lines := []string{
		fmt.Sprintf("Tier %s over %s with %d samples.", st.Tier, formatWindow(st.Window), st.Samples),
		fmt.Sprintf("CPU request = P95 %dm * (1 + %.0f%% headroom) = %dm (was %dm).",
			int64(math.Round(st.CPUMillicores.P95)),
			policy.CPUHeadroom*100,
			proposed.Requests.CPUMillicores,
			current.Requests.CPUMillicores),
		fmt.Sprintf("Memory request = max %s * (1 + %.0f%% headroom) = %s (was %s).",
			formatGi(st.MemoryBytes.Max),
			policy.MemoryHeadroom*100,
			formatGi(float64(proposed.Requests.MemoryBytes)),
			formatGi(float64(current.Requests.MemoryBytes))),
	}

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

// formatGi renders a byte count (as a float64, since usage percentiles are float-valued)
// as gibibytes with one decimal, e.g. "0.9Gi".
func formatGi(bytes float64) string {
	const gib = 1024 * 1024 * 1024
	return fmt.Sprintf("%.1fGi", bytes/float64(gib))
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
