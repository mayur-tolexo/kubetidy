package usage

import (
	"context"
	"errors"
	"testing"

	"github.com/kubetidy/kubetidy/internal/model"
)

// stubProvider is a configurable Provider for composing fallback tests.
type stubProvider struct {
	name  string
	tier  model.EvidenceTier
	stats map[string]model.UsageStats
	err   error
}

func (s *stubProvider) Name() string             { return s.name }
func (s *stubProvider) Tier() model.EvidenceTier { return s.tier }
func (s *stubProvider) Usage(context.Context, model.Workload) (map[string]model.UsageStats, error) {
	return s.stats, s.err
}

func opStats() map[string]model.UsageStats {
	return map[string]model.UsageStats{"app": {Samples: 100, Tier: model.TierOperator}}
}
func snapStats() map[string]model.UsageStats {
	return map[string]model.UsageStats{"app": {Samples: 1, Tier: model.TierSnapshot}}
}

func TestFallback_NameAndTierFromPrimary(t *testing.T) {
	p := NewFallbackProvider(
		&stubProvider{name: "kubetidy operator", tier: model.TierOperator},
		&stubProvider{name: "metrics-server", tier: model.TierSnapshot},
	)
	if p.Name() != "kubetidy operator" {
		t.Errorf("Name = %q, want kubetidy operator", p.Name())
	}
	if p.Tier() != model.TierOperator {
		t.Errorf("Tier = %v, want TierOperator", p.Tier())
	}
}

func TestFallback_UsesPrimaryWhenItHasData(t *testing.T) {
	p := NewFallbackProvider(
		&stubProvider{stats: opStats()},
		&stubProvider{stats: snapStats()},
	)
	got, err := p.Usage(context.Background(), model.Workload{Name: "x"})
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if got["app"].Tier != model.TierOperator {
		t.Errorf("tier = %v, want TierOperator (primary used)", got["app"].Tier)
	}
}

func TestFallback_UsesSecondaryWhenPrimaryEmpty(t *testing.T) {
	p := NewFallbackProvider(
		&stubProvider{stats: map[string]model.UsageStats{}}, // operator hasn't profiled this workload
		&stubProvider{stats: snapStats()},
	)
	got, _ := p.Usage(context.Background(), model.Workload{Name: "x"})
	if got["app"].Tier != model.TierSnapshot {
		t.Errorf("tier = %v, want TierSnapshot (fell back)", got["app"].Tier)
	}
}

func TestFallback_UsesSecondaryWhenPrimaryErrors(t *testing.T) {
	p := NewFallbackProvider(
		&stubProvider{err: errors.New("boom")},
		&stubProvider{stats: snapStats()},
	)
	got, err := p.Usage(context.Background(), model.Workload{Name: "x"})
	if err != nil {
		t.Fatalf("Usage should not surface the primary error when secondary covers it: %v", err)
	}
	if got["app"].Tier != model.TierSnapshot {
		t.Errorf("tier = %v, want TierSnapshot (fell back on primary error)", got["app"].Tier)
	}
}
