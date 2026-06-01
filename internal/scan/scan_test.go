package scan

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kubetidy/kubetidy/internal/model"
)

const gib = 1024 * 1024 * 1024

// fakeUsage is a canned usage.Provider. It returns usageByRef[w.Ref()], or errByRef[w.Ref()]
// when set, letting tests exercise the degrade-on-error path per workload.
type fakeUsage struct {
	tier       model.EvidenceTier
	usageByRef map[string]map[string]model.UsageStats
	errByRef   map[string]error
}

func (f *fakeUsage) Name() string             { return "fake-usage" }
func (f *fakeUsage) Tier() model.EvidenceTier { return f.tier }
func (f *fakeUsage) Usage(_ context.Context, w model.Workload) (map[string]model.UsageStats, error) {
	if err, ok := f.errByRef[w.Ref()]; ok {
		return nil, err
	}
	return f.usageByRef[w.Ref()], nil
}

// fakePricing is a canned pricing.Provider. It returns a fixed price, or err when set.
type fakePricing struct {
	price model.ResourcePrice
	err   error
}

func (f *fakePricing) Name() string { return "fake-pricing" }
func (f *fakePricing) ResourcePrice(_ context.Context, _ model.Workload) (model.ResourcePrice, error) {
	if f.err != nil {
		return model.ResourcePrice{}, f.err
	}
	return f.price, nil
}

// overProvisioned: requests 1000m / 2Gi but only uses ~250m / ~1Gi -> savings expected.
func overProvisioned() model.Workload {
	return model.Workload{
		Kind:      model.KindDeployment,
		Name:      "api",
		Namespace: "default",
		Replicas:  3,
		Containers: []model.Container{
			{
				Name:     "app",
				Requests: model.ResourceAmounts{CPUMillicores: 1000, MemoryBytes: 2 * gib},
				Limits:   model.ResourceAmounts{CPUMillicores: 1000, MemoryBytes: 2 * gib},
			},
		},
	}
}

func overProvisionedStats() map[string]model.UsageStats {
	return map[string]model.UsageStats{
		"app": {
			CPUMillicores: model.Percentiles{P50: 200, P95: 250, Max: 300, CV: 0.2},
			MemoryBytes:   model.Percentiles{P50: 800 * 1024 * 1024, P95: 950 * 1024 * 1024, Max: 1024 * 1024 * 1024, CV: 0.1},
			Window:        14 * 24 * time.Hour,
			Samples:       1200,
			Tier:          model.TierHistorical,
		},
	}
}

func TestRun_ProducesRecommendationsAndScore(t *testing.T) {
	w := overProvisioned()
	eng := &Engine{
		Workloads: []model.Workload{w},
		Usage: &fakeUsage{
			tier:       model.TierHistorical,
			usageByRef: map[string]map[string]model.UsageStats{w.Ref(): overProvisionedStats()},
		},
		Price:   &fakePricing{price: model.ResourcePrice{CPUCoreMonth: 30, MemGiBMonth: 4, Source: "node pricing"}},
		Policy:  model.DefaultPolicy(),
		Context: "kind-test",
	}

	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if res.Context != "kind-test" {
		t.Errorf("Context = %q, want kind-test", res.Context)
	}
	if res.WorkloadCount != 1 {
		t.Errorf("WorkloadCount = %d, want 1", res.WorkloadCount)
	}
	if res.GeneratedAt.IsZero() {
		t.Error("GeneratedAt not set")
	}
	if res.Tier != model.TierHistorical {
		t.Errorf("Tier = %v, want TierHistorical", res.Tier)
	}
	if len(res.Recommendations) != 1 {
		t.Fatalf("len(Recommendations) = %d, want 1", len(res.Recommendations))
	}

	rec := res.Recommendations[0]
	if rec.ContainerName != "app" {
		t.Errorf("ContainerName = %q, want app", rec.ContainerName)
	}
	if rec.Tier != model.TierHistorical {
		t.Errorf("rec.Tier = %v, want TierHistorical", rec.Tier)
	}
	// P95 250m * 1.15 = 287.5 -> 288m, well below the current 1000m request.
	if rec.Proposed.Requests.CPUMillicores >= rec.Current.Requests.CPUMillicores {
		t.Errorf("expected CPU downsizing: proposed %d >= current %d",
			rec.Proposed.Requests.CPUMillicores, rec.Current.Requests.CPUMillicores)
	}
	if rec.MonthlySavings <= 0 {
		t.Errorf("MonthlySavings = %.2f, want > 0", rec.MonthlySavings)
	}
	if rec.Confidence.Score <= 0 {
		t.Errorf("Confidence.Score = %.2f, want > 0", rec.Confidence.Score)
	}
	if !strings.Contains(rec.Evidence, "P95 cpu") || !strings.Contains(rec.Evidence, "samples") {
		t.Errorf("Evidence not composed as expected: %q", rec.Evidence)
	}
	if len(rec.Explanation) == 0 {
		t.Error("Explanation is empty")
	}

	if res.TotalMonthlyWaste <= 0 {
		t.Errorf("TotalMonthlyWaste = %.2f, want > 0", res.TotalMonthlyWaste)
	}
	// TotalMonthlyWaste should equal the single positive saving.
	if got, want := res.TotalMonthlyWaste, rec.MonthlySavings; got != want {
		t.Errorf("TotalMonthlyWaste = %.2f, want %.2f", got, want)
	}
	if res.EfficiencyScore < 0 || res.EfficiencyScore > 100 {
		t.Errorf("EfficiencyScore = %d, out of [0,100]", res.EfficiencyScore)
	}
}

func TestRun_UsageErrorBecomesWarning(t *testing.T) {
	good := overProvisioned()
	bad := model.Workload{
		Kind: model.KindDeployment, Name: "broken", Namespace: "default", Replicas: 1,
		Containers: []model.Container{{Name: "app"}},
	}

	eng := &Engine{
		Workloads: []model.Workload{good, bad},
		Usage: &fakeUsage{
			tier:       model.TierHistorical,
			usageByRef: map[string]map[string]model.UsageStats{good.Ref(): overProvisionedStats()},
			errByRef:   map[string]error{bad.Ref(): errors.New("metrics-server timeout")},
		},
		Price:  &fakePricing{price: model.ResourcePrice{CPUCoreMonth: 30, MemGiBMonth: 4}},
		Policy: model.DefaultPolicy(),
	}

	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned hard error, want graceful degrade: %v", err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("len(Warnings) = %d, want 1; warnings=%v", len(res.Warnings), res.Warnings)
	}
	if !strings.Contains(res.Warnings[0], "broken") || !strings.Contains(res.Warnings[0], "timeout") {
		t.Errorf("warning text unexpected: %q", res.Warnings[0])
	}
	// The healthy workload still yields a recommendation.
	if len(res.Recommendations) != 1 {
		t.Errorf("len(Recommendations) = %d, want 1 (healthy workload only)", len(res.Recommendations))
	}
}

func TestRun_PricingErrorWarnsAndStillRecommends(t *testing.T) {
	w := overProvisioned()
	eng := &Engine{
		Workloads: []model.Workload{w},
		Usage: &fakeUsage{
			tier:       model.TierHistorical,
			usageByRef: map[string]map[string]model.UsageStats{w.Ref(): overProvisionedStats()},
		},
		Price:  &fakePricing{err: errors.New("opencost unreachable")},
		Policy: model.DefaultPolicy(),
	}

	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "pricing") {
		t.Fatalf("expected one pricing warning, got %v", res.Warnings)
	}
	if len(res.Recommendations) != 1 {
		t.Fatalf("len(Recommendations) = %d, want 1", len(res.Recommendations))
	}
	// With zero price, savings collapse to zero and nothing is added to waste.
	if res.Recommendations[0].MonthlySavings != 0 {
		t.Errorf("MonthlySavings = %.2f, want 0 with zero price", res.Recommendations[0].MonthlySavings)
	}
	if res.TotalMonthlyWaste != 0 {
		t.Errorf("TotalMonthlyWaste = %.2f, want 0", res.TotalMonthlyWaste)
	}
}

func TestRun_SkipsContainersWithoutUsage(t *testing.T) {
	w := model.Workload{
		Kind: model.KindDeployment, Name: "multi", Namespace: "default", Replicas: 2,
		Containers: []model.Container{
			{Name: "app", Requests: model.ResourceAmounts{CPUMillicores: 1000, MemoryBytes: 2 * gib}},
			{Name: "sidecar", Requests: model.ResourceAmounts{CPUMillicores: 100}},
		},
	}
	// Only "app" has usage data; "sidecar" must be skipped without a fatal error.
	stats := map[string]model.UsageStats{
		"app": overProvisionedStats()["app"],
	}

	eng := &Engine{
		Workloads: []model.Workload{w},
		Usage:     &fakeUsage{tier: model.TierHistorical, usageByRef: map[string]map[string]model.UsageStats{w.Ref(): stats}},
		Price:     &fakePricing{price: model.ResourcePrice{CPUCoreMonth: 30, MemGiBMonth: 4}},
		Policy:    model.DefaultPolicy(),
	}

	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(res.Recommendations) != 1 {
		t.Fatalf("len(Recommendations) = %d, want 1 (sidecar skipped)", len(res.Recommendations))
	}
	if res.Recommendations[0].ContainerName != "app" {
		t.Errorf("recommendation for %q, want app", res.Recommendations[0].ContainerName)
	}
}

func TestDominantTier(t *testing.T) {
	rec := func(tier model.EvidenceTier) model.Recommendation { return model.Recommendation{Tier: tier} }

	if _, ok := dominantTier(nil); ok {
		t.Error("empty recs should return ok=false")
	}

	// Plurality wins.
	got, ok := dominantTier([]model.Recommendation{
		rec(model.TierOperator), rec(model.TierOperator), rec(model.TierSnapshot),
	})
	if !ok || got != model.TierOperator {
		t.Errorf("dominant = %v (ok=%v), want TierOperator", got, ok)
	}

	// All snapshot (the warm-up case): banner should read snapshot, not the provider's Tier 0.
	got, _ = dominantTier([]model.Recommendation{rec(model.TierSnapshot), rec(model.TierSnapshot)})
	if got != model.TierSnapshot {
		t.Errorf("dominant = %v, want TierSnapshot", got)
	}

	// Tie breaks toward the higher tier.
	got, _ = dominantTier([]model.Recommendation{rec(model.TierSnapshot), rec(model.TierHistorical)})
	if got != model.TierHistorical {
		t.Errorf("tie dominant = %v, want TierHistorical (higher)", got)
	}
}

func TestRun_SnapshotCaveatBasedOnFindings(t *testing.T) {
	w := model.Workload{
		Kind: model.KindDeployment, Name: "web", Namespace: "shop", Replicas: 1,
		Containers: []model.Container{{Name: "app", Requests: model.ResourceAmounts{CPUMillicores: 1000, MemoryBytes: 1 << 30}}},
	}
	stats := map[string]model.UsageStats{"app": {
		CPUMillicores: model.Percentiles{P95: 10}, MemoryBytes: model.Percentiles{Max: 32 << 20},
		Samples: 1, Tier: model.TierSnapshot,
	}}
	eng := &Engine{
		Workloads: []model.Workload{w},
		Usage:     &fakeUsage{tier: model.TierSnapshot, usageByRef: map[string]map[string]model.UsageStats{w.Ref(): stats}},
		Price:     &fakePricing{price: model.ResourcePrice{CPUCoreMonth: 30, MemGiBMonth: 4}},
		Policy:    model.DefaultPolicy(),
	}
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Tier != model.TierSnapshot {
		t.Errorf("Tier = %v, want TierSnapshot", res.Tier)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "snapshot") {
		t.Errorf("want a leading snapshot caveat, got %v", res.Warnings)
	}
}
