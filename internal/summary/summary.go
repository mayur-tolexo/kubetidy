// Package summary builds a per-cluster rollup (ClusterUsageSummary status) from the set of
// per-workload rightsizing recommendations. It is PURE: no I/O, no cluster access — it takes
// the already-computed recommendations + efficiency score and aggregates them into the typed
// status the operator persists and dashboards / Cluster API read.
package summary

import (
	"sort"
	"time"

	"github.com/kubetidy/kubetidy/api/v1alpha1"
	"github.com/kubetidy/kubetidy/internal/model"
)

// Options tunes the rollup.
type Options struct {
	// TopN caps how many improvement targets are listed (0 = a sensible default).
	TopN int
	// GeneratedAt stamps the summary; zero means "no timestamp".
	GeneratedAt time.Time
}

const defaultTopN = 20

// Build aggregates per-workload recommendations into a ClusterUsageSummaryStatus. currentCost
// is a per-recommendation function giving the workload's current monthly cost (so total spend
// can be reported); pass nil to report only wasted cost. score is the precomputed cluster
// efficiency score (0..100).
func Build(recs []model.Recommendation, score int, currentCost func(model.Recommendation) float64, opts Options) v1alpha1.ClusterUsageSummaryStatus {
	topN := opts.TopN
	if topN <= 0 {
		topN = defaultTopN
	}

	status := v1alpha1.ClusterUsageSummaryStatus{
		EfficiencyScore: clampScore(score),
		WorkloadCount:   countWorkloads(recs),
	}
	if !opts.GeneratedAt.IsZero() {
		status.GeneratedAt = opts.GeneratedAt.UTC().Format(time.RFC3339)
	}

	for _, r := range recs {
		if currentCost != nil {
			status.TotalMonthlyCost += currentCost(r)
		}
		if r.MonthlySavings > 0 {
			status.WastedMonthlyCost += r.MonthlySavings
		}
	}

	status.TopTargets = topTargets(recs, topN)
	return status
}

// countWorkloads counts distinct workloads (recommendations are per-container, so several may
// share a workload).
func countWorkloads(recs []model.Recommendation) int {
	seen := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		seen[r.Workload.Ref()] = struct{}{}
	}
	return len(seen)
}

// topTargets ranks the highest-savings improvement opportunities, aggregated per workload, and
// returns the top n.
func topTargets(recs []model.Recommendation, n int) []v1alpha1.WorkloadTarget {
	// Aggregate per workload: sum savings across its containers, keep the max confidence.
	type agg struct {
		ref        model.Workload
		savings    float64
		confidence int
	}
	byWorkload := map[string]*agg{}
	order := []string{}
	for _, r := range recs {
		if r.MonthlySavings <= 0 {
			continue // only positive savings are "targets"
		}
		key := r.Workload.Ref()
		a := byWorkload[key]
		if a == nil {
			a = &agg{ref: r.Workload}
			byWorkload[key] = a
			order = append(order, key)
		}
		a.savings += r.MonthlySavings
		if c := r.Confidence.Percent(); c > a.confidence {
			a.confidence = c
		}
	}

	targets := make([]v1alpha1.WorkloadTarget, 0, len(order))
	for _, key := range order {
		a := byWorkload[key]
		targets = append(targets, v1alpha1.WorkloadTarget{
			TargetRef:      v1alpha1.TargetRef{Kind: string(a.ref.Kind), Name: a.ref.Name},
			Namespace:      a.ref.Namespace,
			MonthlySavings: a.savings,
			Confidence:     a.confidence,
		})
	}

	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].MonthlySavings != targets[j].MonthlySavings {
			return targets[i].MonthlySavings > targets[j].MonthlySavings
		}
		return targets[i].Namespace+targets[i].TargetRef.Name < targets[j].Namespace+targets[j].TargetRef.Name
	})
	if n > 0 && n < len(targets) {
		targets = targets[:n]
	}
	return targets
}

func clampScore(s int) int {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}
