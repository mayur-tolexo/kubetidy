package summary

import (
	"testing"
	"time"

	"github.com/kubetidy/kubetidy/internal/model"
)

func rec(kind, ns, name string, savings float64, conf float64) model.Recommendation {
	return model.Recommendation{
		Workload:       model.Workload{Kind: model.WorkloadKind(kind), Name: name, Namespace: ns},
		MonthlySavings: savings,
		Confidence:     model.Confidence{Score: conf},
	}
}

func TestBuildRollup(t *testing.T) {
	recs := []model.Recommendation{
		rec("Deployment", "shop", "checkout", 100, 0.9),
		rec("Deployment", "shop", "search", 50, 0.8),
		rec("Deployment", "shop", "grow-me", -20, 0.5), // negative savings: not a target, not waste
	}
	current := func(model.Recommendation) float64 { return 200 } // each workload currently $200

	status := Build(recs, 73, current, Options{GeneratedAt: time.Unix(0, 0)})

	if status.EfficiencyScore != 73 {
		t.Errorf("score = %d, want 73", status.EfficiencyScore)
	}
	if status.WorkloadCount != 3 {
		t.Errorf("workloadCount = %d, want 3", status.WorkloadCount)
	}
	if status.WastedMonthlyCost != 150 { // 100 + 50; the -20 is not waste
		t.Errorf("wasted = %v, want 150", status.WastedMonthlyCost)
	}
	if status.TotalMonthlyCost != 600 { // 3 * 200
		t.Errorf("total = %v, want 600", status.TotalMonthlyCost)
	}
	if status.GeneratedAt == "" {
		t.Error("expected a GeneratedAt timestamp")
	}
	// Top targets exclude the negative-savings workload and are sorted by savings desc.
	if len(status.TopTargets) != 2 {
		t.Fatalf("topTargets = %d, want 2", len(status.TopTargets))
	}
	if status.TopTargets[0].TargetRef.Name != "checkout" || status.TopTargets[0].MonthlySavings != 100 {
		t.Errorf("top target = %+v, want checkout/$100", status.TopTargets[0])
	}
	if status.TopTargets[0].Confidence != 90 {
		t.Errorf("top target confidence = %d, want 90", status.TopTargets[0].Confidence)
	}
}

func TestBuildAggregatesContainersPerWorkload(t *testing.T) {
	// Two containers of the same workload: savings sum, max confidence wins.
	recs := []model.Recommendation{
		rec("Deployment", "ns", "api", 30, 0.6),
		rec("Deployment", "ns", "api", 70, 0.9),
	}
	status := Build(recs, 50, nil, Options{})
	if status.WorkloadCount != 1 {
		t.Errorf("workloadCount = %d, want 1", status.WorkloadCount)
	}
	if len(status.TopTargets) != 1 || status.TopTargets[0].MonthlySavings != 100 {
		t.Fatalf("expected one target summing to $100, got %+v", status.TopTargets)
	}
	if status.TopTargets[0].Confidence != 90 {
		t.Errorf("confidence = %d, want 90 (max of containers)", status.TopTargets[0].Confidence)
	}
}

func TestBuildTopNAndScoreClamp(t *testing.T) {
	var recs []model.Recommendation
	for i := 0; i < 5; i++ {
		recs = append(recs, rec("Deployment", "ns", string(rune('a'+i)), float64(i+1), 0.5))
	}
	status := Build(recs, 250, nil, Options{TopN: 2}) // score clamps to 100
	if status.EfficiencyScore != 100 {
		t.Errorf("score = %d, want clamp to 100", status.EfficiencyScore)
	}
	if len(status.TopTargets) != 2 {
		t.Errorf("topTargets = %d, want 2 (TopN)", len(status.TopTargets))
	}
	// Highest savings first.
	if status.TopTargets[0].MonthlySavings != 5 || status.TopTargets[1].MonthlySavings != 4 {
		t.Errorf("targets not sorted desc: %+v", status.TopTargets)
	}
}

func TestBuildEmptyAndNegativeScore(t *testing.T) {
	status := Build(nil, -10, nil, Options{})
	if status.EfficiencyScore != 0 {
		t.Errorf("score = %d, want clamp to 0", status.EfficiencyScore)
	}
	if status.WorkloadCount != 0 || len(status.TopTargets) != 0 {
		t.Error("empty recs should yield empty rollup")
	}
	if status.GeneratedAt != "" {
		t.Error("zero GeneratedAt should leave the field empty")
	}
}
