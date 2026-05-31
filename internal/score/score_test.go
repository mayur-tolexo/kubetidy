package score

import (
	"testing"

	"github.com/kubetidy/kubetidy/internal/model"
)

const giB = 1024 * 1024 * 1024

func amounts(cpuMilli, memBytes int64) model.ResourceAmounts {
	return model.ResourceAmounts{CPUMillicores: cpuMilli, MemoryBytes: memBytes}
}

func reqSpec(a model.ResourceAmounts) model.ResourceSpec {
	return model.ResourceSpec{Requests: a}
}

func rec(current, proposed model.ResourceAmounts) model.Recommendation {
	return model.Recommendation{Current: reqSpec(current), Proposed: reqSpec(proposed)}
}

func factorByName(factors []model.ScoreFactor, name string) (model.ScoreFactor, bool) {
	for _, f := range factors {
		if f.Name == name {
			return f, true
		}
	}
	return model.ScoreFactor{}, false
}

func TestCompute(t *testing.T) {
	tests := []struct {
		name      string
		result    model.ScanResult
		minScore  int
		maxScore  int
		minFactor int
	}{
		{
			name:      "empty result scores 100",
			result:    model.ScanResult{},
			minScore:  100,
			maxScore:  100,
			minFactor: 1,
		},
		{
			name: "perfectly sized cluster scores ~100",
			result: model.ScanResult{
				Recommendations: []model.Recommendation{
					rec(amounts(500, 1*giB), amounts(500, 1*giB)),
					rec(amounts(250, 512*1024*1024), amounts(250, 512*1024*1024)),
				},
			},
			minScore:  99,
			maxScore:  100,
			minFactor: 2,
		},
		{
			name: "heavily over-provisioned scores low",
			result: model.ScanResult{
				Recommendations: []model.Recommendation{
					// asking 10x what is proposed
					rec(amounts(5000, 10*giB), amounts(500, 1*giB)),
					rec(amounts(8000, 16*giB), amounts(800, 1600*1024*1024)),
				},
			},
			minScore:  0,
			maxScore:  45,
			minFactor: 2,
		},
		{
			name: "missing requests penalised via coverage and accuracy",
			result: model.ScanResult{
				Recommendations: []model.Recommendation{
					rec(amounts(0, 0), amounts(500, 1*giB)),
					rec(amounts(0, 0), amounts(500, 1*giB)),
				},
			},
			minScore:  0,
			maxScore:  5,
			minFactor: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, factors := Compute(tt.result)
			if got < tt.minScore || got > tt.maxScore {
				t.Errorf("score = %d, want in [%d,%d]", got, tt.minScore, tt.maxScore)
			}
			if len(factors) < tt.minFactor {
				t.Errorf("got %d factors, want at least %d", len(factors), tt.minFactor)
			}
			for _, f := range factors {
				if f.Name == "" {
					t.Errorf("factor with empty name: %+v", f)
				}
				if f.Description == "" {
					t.Errorf("factor %q has empty description", f.Name)
				}
				if f.Value < 0 || f.Value > 1 {
					t.Errorf("factor %q value %v out of [0,1]", f.Name, f.Value)
				}
			}
		})
	}
}

func TestComputeClamped(t *testing.T) {
	got, _ := Compute(model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(amounts(500, 1*giB), amounts(500, 1*giB)),
		},
	})
	if got < 0 || got > 100 {
		t.Errorf("score %d not clamped to [0,100]", got)
	}
}

func TestEmptyResultFactorExplains(t *testing.T) {
	score, factors := Compute(model.ScanResult{})
	if score != 100 {
		t.Fatalf("empty score = %d, want 100", score)
	}
	if len(factors) != 1 {
		t.Fatalf("empty result want 1 factor, got %d", len(factors))
	}
	if factors[0].Value != 1.0 {
		t.Errorf("empty factor value = %v, want 1.0", factors[0].Value)
	}
}

func TestBreakdownFactorsPresent(t *testing.T) {
	_, factors := Compute(model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(amounts(500, 1*giB), amounts(400, 800*1024*1024)),
		},
	})
	if _, ok := factorByName(factors, "Request accuracy"); !ok {
		t.Error("missing 'Request accuracy' factor")
	}
	if _, ok := factorByName(factors, "Coverage (requests set)"); !ok {
		t.Error("missing 'Coverage (requests set)' factor")
	}
}

func TestUnderAndOverProvisionSymmetric(t *testing.T) {
	// 2x too big and 2x too small should yield the same accuracy.
	over := accuracy(amounts(1000, 0), amounts(500, 0))
	under := accuracy(amounts(500, 0), amounts(1000, 0))
	if over != under {
		t.Errorf("accuracy not symmetric: over=%v under=%v", over, under)
	}
	if over <= 0 || over >= 1 {
		t.Errorf("2x mismatch accuracy = %v, want in (0,1)", over)
	}
}

func TestCoveragePartial(t *testing.T) {
	// One workload with requests, one without => coverage 0.5.
	_, factors := Compute(model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(amounts(500, 1*giB), amounts(500, 1*giB)),
			rec(amounts(0, 0), amounts(500, 1*giB)),
		},
	})
	cov, ok := factorByName(factors, "Coverage (requests set)")
	if !ok {
		t.Fatal("missing coverage factor")
	}
	if cov.Value != 0.5 {
		t.Errorf("coverage = %v, want 0.5", cov.Value)
	}
}
