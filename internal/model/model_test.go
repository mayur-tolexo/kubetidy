package model

import "testing"

func TestEvidenceTierString(t *testing.T) {
	cases := map[EvidenceTier]string{
		TierStatic:       "static (no usage data)",
		TierSnapshot:     "snapshot (metrics-server, limited)",
		TierOperator:     "0 (kubetidy operator)",
		TierHistorical:   "1 (Prometheus)",
		TierAllocated:    "2 (OpenCost)",
		EvidenceTier(99): "unknown",
	}
	for tier, want := range cases {
		if got := tier.String(); got != want {
			t.Errorf("EvidenceTier(%d).String() = %q, want %q", tier, got, want)
		}
	}
}

func TestWorkloadRef(t *testing.T) {
	w := Workload{Kind: KindDeployment, Namespace: "shop", Name: "checkout"}
	if got, want := w.Ref(), "Deployment/shop/checkout"; got != want {
		t.Errorf("Ref() = %q, want %q", got, want)
	}
}

func TestResourceAmountsIsZero(t *testing.T) {
	if !(ResourceAmounts{}).IsZero() {
		t.Error("empty ResourceAmounts should be zero")
	}
	if (ResourceAmounts{CPUMillicores: 1}).IsZero() {
		t.Error("ResourceAmounts with CPU set should not be zero")
	}
	if (ResourceAmounts{MemoryBytes: 1}).IsZero() {
		t.Error("ResourceAmounts with memory set should not be zero")
	}
}

func TestConfidencePercent(t *testing.T) {
	cases := map[float64]int{0: 0, 0.5: 50, 0.955: 96, 1: 100}
	for score, want := range cases {
		if got := (Confidence{Score: score}).Percent(); got != want {
			t.Errorf("Confidence{%v}.Percent() = %d, want %d", score, got, want)
		}
	}
}

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	if p.CPUHeadroom != 0.15 || p.MemoryHeadroom != 0.15 {
		t.Errorf("default headroom = %v/%v, want 0.15/0.15", p.CPUHeadroom, p.MemoryHeadroom)
	}
	if p.SetCPULimit {
		t.Error("default policy should not set a CPU limit (avoid throttling)")
	}
	if !p.MemoryLimitEqualsRequest {
		t.Error("default policy should set memory limit == request")
	}
	if p.SnapshotHeadroom <= 0 || p.MinCPURequestMillicores <= 0 || p.MinMemoryRequestBytes <= 0 {
		t.Error("snapshot-safety knobs should be enabled by default")
	}
	if !p.DownsizeOnlyOnSnapshot {
		t.Error("default policy should be downsize-only on snapshot data")
	}
}

func TestConfidenceBand(t *testing.T) {
	cases := []struct {
		score float64
		want  ConfidenceBand
	}{
		{0.05, ConfidenceLow},
		{0.59, ConfidenceLow},
		{0.60, ConfidenceMedium},
		{0.79, ConfidenceMedium},
		{0.80, ConfidenceHigh},
		{0.99, ConfidenceHigh},
	}
	for _, c := range cases {
		if got := (Confidence{Score: c.score}).Band(); got != c.want {
			t.Errorf("Band(%.2f) = %q, want %q", c.score, got, c.want)
		}
	}
}
