package rightsizer

import (
	"testing"
	"time"

	"github.com/kubetidy/kubetidy/internal/model"
)

func amounts(cpu, mem int64) model.ResourceAmounts {
	return model.ResourceAmounts{CPUMillicores: cpu, MemoryBytes: mem}
}

func TestRecommend(t *testing.T) {
	tests := []struct {
		name    string
		current model.ResourceSpec
		usage   model.UsageStats
		policy  model.Policy
		want    model.ResourceSpec
	}{
		{
			name: "normal tier-1 default policy: cpu request only, mem req==limit",
			current: model.ResourceSpec{
				Requests: amounts(2000, 4*1024*mib),
				Limits:   amounts(0, 4*1024*mib),
			},
			usage: model.UsageStats{
				CPUMillicores: model.Percentiles{P95: 280},
				MemoryBytes:   model.Percentiles{Max: 900 * mib},
				Tier:          model.TierHistorical,
			},
			policy: model.DefaultPolicy(),
			want: model.ResourceSpec{
				// 280 * 1.15 = 322
				Requests: amounts(322, roundUpMiB(int64(900*mib*1.15))),
				// no CPU limit by default; mem limit == request
				Limits: amounts(0, roundUpMiB(int64(900*mib*1.15))),
			},
		},
		{
			name: "SetCPULimit true: cpu limit == cpu request",
			current: model.ResourceSpec{
				Requests: amounts(0, 0),
				Limits:   amounts(0, 0),
			},
			usage: model.UsageStats{
				CPUMillicores: model.Percentiles{P95: 100},
				MemoryBytes:   model.Percentiles{Max: 10 * mib},
				Tier:          model.TierHistorical,
			},
			policy: model.Policy{CPUHeadroom: 0, MemoryHeadroom: 0, SetCPULimit: true, MemoryLimitEqualsRequest: true},
			want: model.ResourceSpec{
				Requests: amounts(100, 10*mib),
				Limits:   amounts(100, 10*mib),
			},
		},
		{
			name: "memory limit carries over current when not equals-request",
			current: model.ResourceSpec{
				Requests: amounts(500, 1*mib),
				Limits:   amounts(0, 2048*mib),
			},
			usage: model.UsageStats{
				CPUMillicores: model.Percentiles{P95: 200},
				MemoryBytes:   model.Percentiles{Max: 500 * mib},
				Tier:          model.TierHistorical,
			},
			policy: model.Policy{CPUHeadroom: 0, MemoryHeadroom: 0, SetCPULimit: false, MemoryLimitEqualsRequest: false},
			want: model.ResourceSpec{
				Requests: amounts(200, 500*mib),
				Limits:   amounts(0, 2048*mib), // carried over
			},
		},
		{
			name: "zero usage for both metrics keeps current",
			current: model.ResourceSpec{
				Requests: amounts(750, 256*mib),
				Limits:   amounts(1500, 512*mib),
			},
			usage:  model.UsageStats{Tier: model.TierStatic},
			policy: model.DefaultPolicy(),
			want: model.ResourceSpec{
				Requests: amounts(750, 256*mib),
				Limits:   amounts(1500, 512*mib),
			},
		},
		{
			name: "zero current and zero usage stays zero",
			current: model.ResourceSpec{
				Requests: amounts(0, 0),
				Limits:   amounts(0, 0),
			},
			usage:  model.UsageStats{Tier: model.TierStatic},
			policy: model.DefaultPolicy(),
			want: model.ResourceSpec{
				Requests: amounts(0, 0),
				Limits:   amounts(0, 0),
			},
		},
		{
			name: "zero cpu usage keeps current cpu but recommends memory",
			current: model.ResourceSpec{
				Requests: amounts(900, 128*mib),
				Limits:   amounts(1800, 128*mib),
			},
			usage: model.UsageStats{
				CPUMillicores: model.Percentiles{P95: 0},
				MemoryBytes:   model.Percentiles{Max: 200 * mib},
				Tier:          model.TierHistorical,
			},
			policy: model.Policy{CPUHeadroom: 0.15, MemoryHeadroom: 0, SetCPULimit: false, MemoryLimitEqualsRequest: true},
			want: model.ResourceSpec{
				Requests: amounts(900, 200*mib),
				Limits:   amounts(1800, 200*mib),
			},
		},
		{
			name: "zero memory usage keeps current memory but recommends cpu",
			current: model.ResourceSpec{
				Requests: amounts(100, 64*mib),
				Limits:   amounts(0, 128*mib),
			},
			usage: model.UsageStats{
				CPUMillicores: model.Percentiles{P95: 400},
				MemoryBytes:   model.Percentiles{Max: 0},
				Tier:          model.TierHistorical,
			},
			policy: model.Policy{CPUHeadroom: 0, MemoryHeadroom: 0.15, SetCPULimit: false, MemoryLimitEqualsRequest: true},
			want: model.ResourceSpec{
				Requests: amounts(400, 64*mib),
				Limits:   amounts(0, 128*mib),
			},
		},
		{
			name:    "headroom math rounds cpu to whole millicores",
			current: model.ResourceSpec{},
			usage: model.UsageStats{
				CPUMillicores: model.Percentiles{P95: 333},
				MemoryBytes:   model.Percentiles{Max: 1},
				Tier:          model.TierHistorical,
			},
			policy: model.Policy{CPUHeadroom: 0.15, MemoryHeadroom: 0, MemoryLimitEqualsRequest: true},
			want: model.ResourceSpec{
				// 333 * 1.15 = 382.95 -> 383
				Requests: amounts(383, mib), // 1 byte rounds up to 1 MiB
				Limits:   amounts(0, mib),
			},
		},
		{
			name:    "memory rounds up to nearest MiB",
			current: model.ResourceSpec{},
			usage: model.UsageStats{
				CPUMillicores: model.Percentiles{P95: 10},
				MemoryBytes:   model.Percentiles{Max: mib + 1}, // just over 1 MiB
				Tier:          model.TierHistorical,
			},
			policy: model.Policy{CPUHeadroom: 0, MemoryHeadroom: 0, SetCPULimit: true, MemoryLimitEqualsRequest: true},
			want: model.ResourceSpec{
				Requests: amounts(10, 2*mib),
				Limits:   amounts(10, 2*mib),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Recommend(tt.current, tt.usage, tt.policy)
			if got != tt.want {
				t.Errorf("Recommend() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRecommendNeverNegative(t *testing.T) {
	got := Recommend(model.ResourceSpec{}, model.UsageStats{
		CPUMillicores: model.Percentiles{P95: -50},
		MemoryBytes:   model.Percentiles{Max: -100},
		Tier:          model.TierHistorical,
	}, model.DefaultPolicy())
	if got.Requests.CPUMillicores < 0 || got.Requests.MemoryBytes < 0 ||
		got.Limits.CPUMillicores < 0 || got.Limits.MemoryBytes < 0 {
		t.Errorf("Recommend produced negative values: %+v", got)
	}
}

func TestConfidence(t *testing.T) {
	const day = 24 * time.Hour

	tests := []struct {
		name      string
		usage     model.UsageStats
		minScore  float64
		maxScore  float64
		wantExact float64 // 0 means skip exact check
	}{
		{
			name: "tier-1 14d low variance high confidence",
			usage: model.UsageStats{
				Tier:          model.TierHistorical,
				Window:        14 * day,
				Samples:       1_200_000,
				CPUMillicores: model.Percentiles{CV: 0.1},
				MemoryBytes:   model.Percentiles{CV: 0.05},
			},
			minScore: 0.93,
			maxScore: 0.99,
		},
		{
			name: "snapshot single sample capped at 0.6",
			usage: model.UsageStats{
				Tier:    model.TierSnapshot,
				Window:  0,
				Samples: 1,
			},
			minScore: 0.45,
			maxScore: 0.60,
		},
		{
			name: "snapshot can never exceed 0.6 even with long window",
			usage: model.UsageStats{
				Tier:    model.TierSnapshot,
				Window:  30 * day,
				Samples: 100000,
			},
			minScore: 0.05,
			maxScore: 0.60,
		},
		{
			name: "static tier base",
			usage: model.UsageStats{
				Tier: model.TierStatic,
			},
			wantExact: 0.25,
		},
		{
			name: "allocated tier base high",
			usage: model.UsageStats{
				Tier:    model.TierAllocated,
				Window:  14 * day,
				Samples: 1000,
			},
			minScore: 0.90,
			maxScore: 0.99,
		},
		{
			name: "high variance penalizes score and clamps to floor",
			usage: model.UsageStats{
				Tier:          model.TierStatic,
				CPUMillicores: model.Percentiles{CV: 2.0},
			},
			wantExact: 0.05,
		},
		{
			name: "high variance reduces tier-1 confidence",
			usage: model.UsageStats{
				Tier:          model.TierHistorical,
				Window:        14 * day,
				Samples:       100000,
				CPUMillicores: model.Percentiles{CV: 1.0},
			},
			minScore: 0.65,
			maxScore: 0.75,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Confidence(tt.usage)
			if got.Score < 0.05 || got.Score > 0.99 {
				t.Errorf("Confidence score %v out of clamp range [0.05, 0.99]", got.Score)
			}
			if tt.wantExact != 0 {
				if !floatEq(got.Score, tt.wantExact) {
					t.Errorf("Confidence score = %v, want exact %v", got.Score, tt.wantExact)
				}
			} else {
				if got.Score < tt.minScore || got.Score > tt.maxScore {
					t.Errorf("Confidence score = %v, want in [%v, %v]", got.Score, tt.minScore, tt.maxScore)
				}
			}
			if got.Reason == "" {
				t.Error("Confidence reason must not be empty")
			}
		})
	}
}

func TestConfidenceVarianceMonotonic(t *testing.T) {
	base := model.UsageStats{Tier: model.TierHistorical, Window: 14 * 24 * time.Hour, Samples: 10000}
	low := base
	low.CPUMillicores.CV = 0.1
	high := base
	high.CPUMillicores.CV = 0.9

	if Confidence(high).Score >= Confidence(low).Score {
		t.Errorf("higher variance should lower confidence: high=%v low=%v",
			Confidence(high).Score, Confidence(low).Score)
	}
}

func TestConfidenceWindowMonotonic(t *testing.T) {
	base := model.UsageStats{Tier: model.TierHistorical, Samples: 10000, CPUMillicores: model.Percentiles{CV: 0.1}}
	short := base
	short.Window = 1 * 24 * time.Hour
	long := base
	long.Window = 14 * 24 * time.Hour

	if Confidence(long).Score <= Confidence(short).Score {
		t.Errorf("longer window should raise confidence: long=%v short=%v",
			Confidence(long).Score, Confidence(short).Score)
	}
}

func floatEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
