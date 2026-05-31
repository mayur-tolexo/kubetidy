// Package score computes the Cluster Efficiency Score (0..100) from a scan result. It is a
// PURE package: no I/O, deterministic, table-tested.
package score

import (
	"math"

	"github.com/kubetidy/kubetidy/internal/model"
)

// giBytes is one binary gibibyte, used by the internal cost proxy.
const giBytes = 1024 * 1024 * 1024

// memWeight scales memory relative to CPU in the dimensionless cost proxy. It is a weighting
// constant only (not dollars); pricing lives in internal/costmodel, which we deliberately do
// not import here to keep this package self-contained.
const memWeight = 0.10

// scoring weights (must sum to 1.0).
const (
	accuracyWeight = 0.70
	coverageWeight = 0.30
)

// Compute returns the efficiency score (0..100, higher is better) and the explainable
// breakdown of factors that produced it.
//
// The score blends two factors:
//   - Request accuracy: how closely current requests match the recommended (proposed)
//     requests, as a cost-weighted mean across workloads. Both over- and under-provisioning
//     are penalised by distance from a perfect match.
//   - Coverage: the fraction of workloads that actually declare resource requests.
//
// A cluster with no recommendations (nothing to optimize) scores 100.
func Compute(result model.ScanResult) (int, []model.ScoreFactor) {
	recs := result.Recommendations
	if len(recs) == 0 {
		return 100, []model.ScoreFactor{{
			Name:        "Nothing to optimize",
			Value:       1.0,
			Description: "No workloads with usage data to rightsize; cluster scored as fully efficient.",
		}}
	}

	var accWeighted, weightSum float64
	covered := 0
	for _, r := range recs {
		a := accuracy(r.Current.Requests, r.Proposed.Requests)
		w := costProxy(r.Proposed.Requests)
		accWeighted += a * w
		weightSum += w
		if !r.Current.Requests.IsZero() {
			covered++
		}
	}

	var accMean float64
	if weightSum > 0 {
		accMean = accWeighted / weightSum
	} else {
		// All proposals are zero-cost: fall back to an unweighted mean.
		for _, r := range recs {
			accMean += accuracy(r.Current.Requests, r.Proposed.Requests)
		}
		accMean /= float64(len(recs))
	}

	coverage := float64(covered) / float64(len(recs))

	score := int(math.Round((accMean*accuracyWeight + coverage*coverageWeight) * 100))
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	factors := []model.ScoreFactor{
		{
			Name:        "Request accuracy",
			Value:       clamp01(accMean),
			Description: "How closely current requests match the recommended sizing (cost-weighted across workloads).",
		},
		{
			Name:        "Coverage (requests set)",
			Value:       clamp01(coverage),
			Description: "Fraction of workloads that declare resource requests.",
		},
	}
	return score, factors
}

// accuracy measures how close current is to proposed, symmetrically: 1.0 means a perfect
// match, values approach 0 as the two diverge in either direction (over- or
// under-provisioning). Workloads with no requests (zero cost) score 0.
func accuracy(current, proposed model.ResourceAmounts) float64 {
	c := costProxy(current)
	p := costProxy(proposed)
	hi := math.Max(c, p)
	if hi <= 0 {
		return 0
	}
	return math.Min(c, p) / hi
}

// costProxy is a dimensionless weighting of a resource request used for accuracy weighting
// and within accuracy itself. It is NOT a dollar figure.
func costProxy(a model.ResourceAmounts) float64 {
	return float64(a.CPUMillicores)/1000.0 + memWeight*float64(a.MemoryBytes)/giBytes
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
