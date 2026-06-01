// Package recommend converts kubetidy's internal per-container recommendations into the typed,
// rankable Recommendation custom resources external consumers (and a future LLM recommender)
// read. It is PURE: no I/O — it maps model.Recommendation values to v1alpha1.Recommendation
// objects, one per workload, aggregating that workload's containers.
//
// Today the rules engine (rightsizer) produces the input recommendations and this package
// stamps Source=rules. An LLM recommender would produce the same v1alpha1.Recommendation shape
// with Source=llm — a new writer, not a schema change.
package recommend

import (
	"time"

	"github.com/kubetidy/kubetidy/api/v1alpha1"
	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
	"github.com/kubetidy/kubetidy/internal/model"
)

// Options tunes the conversion.
type Options struct {
	// Source records what produced the recommendations (rules today, llm later).
	Source v1alpha1.RecommendationSource
	// GeneratedAt stamps each recommendation; zero means "no timestamp".
	GeneratedAt time.Time
}

// Built pairs a Recommendation object with the lookup name it should be stored under, so the
// caller can upsert without re-deriving the name.
type Built struct {
	Name           string
	Namespace      string
	Recommendation v1alpha1.Recommendation
}

// Build groups per-container model recommendations by workload and produces one typed
// v1alpha1.Recommendation per workload. Workloads with no positive savings still produce an
// object (score reflects that), so consumers see a complete picture and stale objects can be
// reconciled away by the caller.
func Build(recs []model.Recommendation, opts Options) []Built {
	source := opts.Source
	if source == "" {
		source = v1alpha1.SourceRules
	}
	generatedAt := ""
	if !opts.GeneratedAt.IsZero() {
		generatedAt = opts.GeneratedAt.UTC().Format(time.RFC3339)
	}

	// Group by workload, preserving first-seen order for deterministic output.
	type group struct {
		workload model.Workload
		recs     []model.Recommendation
	}
	byWorkload := map[string]*group{}
	order := []string{}
	for _, r := range recs {
		key := r.Workload.Ref()
		g := byWorkload[key]
		if g == nil {
			g = &group{workload: r.Workload}
			byWorkload[key] = g
			order = append(order, key)
		}
		g.recs = append(g.recs, r)
	}

	out := make([]Built, 0, len(order))
	for _, key := range order {
		g := byWorkload[key]
		out = append(out, buildOne(g.workload, g.recs, source, generatedAt))
	}
	return out
}

// buildOne assembles a single Recommendation from one workload's container recommendations.
func buildOne(w model.Workload, recs []model.Recommendation, source v1alpha1.RecommendationSource, generatedAt string) Built {
	name := objectName(w)

	var (
		totalSavings float64
		maxConf      int
		changes      []v1alpha1.ResourceChange
		evidence     []string
	)
	for _, r := range recs {
		totalSavings += r.MonthlySavings
		if c := r.Confidence.Percent(); c > maxConf {
			maxConf = c
		}
		changes = append(changes, v1alpha1.ResourceChange{
			Container:            r.ContainerName,
			CPURequestMillicores: r.Proposed.Requests.CPUMillicores,
			MemoryRequestBytes:   r.Proposed.Requests.MemoryBytes,
		})
		if r.Evidence != "" {
			evidence = append(evidence, r.ContainerName+": "+r.Evidence)
		}
	}

	rec := v1alpha1.Recommendation{}
	rec.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Recommendation"))
	rec.Name = name
	rec.Namespace = w.Namespace
	rec.Spec = v1alpha1.RecommendationSpec{
		TargetRef:   v1alpha1.TargetRef{Kind: string(w.Kind), Name: w.Name},
		Namespace:   w.Namespace,
		Source:      source,
		GeneratedAt: generatedAt,
		InputsRef:   objectName(w), // the UsageProfile this was derived from (same name scheme)
	}
	rec.Status = v1alpha1.RecommendationStatus{
		Score:          scoreFor(totalSavings, maxConf),
		Confidence:     maxConf,
		MonthlySavings: totalSavings,
		Changes:        changes,
		Evidence:       evidence,
	}
	return Built{Name: name, Namespace: w.Namespace, Recommendation: rec}
}

// scoreFor ranks a recommendation 0..100: savings make it more desirable, weighted by
// confidence. A non-positive-savings recommendation scores 0 (nothing worth doing). The curve
// saturates so a handful of high-value workloads don't all flatten to 100.
func scoreFor(monthlySavings float64, confidence int) int {
	if monthlySavings <= 0 {
		return 0
	}
	// Savings component: $100/mo -> ~50, $400/mo -> ~80, saturating toward 100.
	savingsScore := 100.0 * monthlySavings / (monthlySavings + 100.0)
	// Weight by confidence (0..1); a low-confidence rec is less actionable.
	conf := float64(confidence) / 100.0
	score := int(savingsScore*conf + 0.5)
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

// objectName is the shared lowercase RFC 1123 name for a workload's resources, matching the
// UsageProfile naming so a Recommendation and its source UsageProfile share a name.
func objectName(w model.Workload) string {
	return usageprofile.ObjectName(string(w.Kind), w.Name)
}
