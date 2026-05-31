package costmodel

import (
	"math"
	"testing"

	"github.com/kubetidy/kubetidy/internal/model"
)

const eps = 1e-9

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) <= eps
}

func spec(cpuMilli, memBytes int64) model.ResourceSpec {
	return model.ResourceSpec{
		Requests: model.ResourceAmounts{CPUMillicores: cpuMilli, MemoryBytes: memBytes},
		// Limits intentionally set high to prove they are ignored by cost.
		Limits: model.ResourceAmounts{CPUMillicores: 99999, MemoryBytes: 99 * bytesPerGiB},
	}
}

func TestMonthlyCost(t *testing.T) {
	tests := []struct {
		name  string
		spec  model.ResourceSpec
		price model.ResourcePrice
		want  float64
	}{
		{
			name:  "one core one GiB exact unit conversion",
			spec:  spec(1000, bytesPerGiB),
			price: model.ResourcePrice{CPUCoreMonth: 10, MemGiBMonth: 4},
			want:  10 + 4,
		},
		{
			name:  "half core half GiB",
			spec:  spec(500, bytesPerGiB/2),
			price: model.ResourcePrice{CPUCoreMonth: 10, MemGiBMonth: 4},
			want:  5 + 2,
		},
		{
			name:  "cpu only (zero memory request)",
			spec:  spec(250, 0),
			price: model.ResourcePrice{CPUCoreMonth: 40, MemGiBMonth: 4},
			want:  10,
		},
		{
			name:  "memory only (zero cpu request)",
			spec:  spec(0, 2*bytesPerGiB),
			price: model.ResourcePrice{CPUCoreMonth: 40, MemGiBMonth: 4},
			want:  8,
		},
		{
			name:  "zero price yields zero cost",
			spec:  spec(2000, 4*bytesPerGiB),
			price: model.ResourcePrice{CPUCoreMonth: 0, MemGiBMonth: 0},
			want:  0,
		},
		{
			name:  "negative prices clamped to zero",
			spec:  spec(1000, bytesPerGiB),
			price: model.ResourcePrice{CPUCoreMonth: -10, MemGiBMonth: -4},
			want:  0,
		},
		{
			name:  "GiB conversion is binary not decimal",
			spec:  spec(0, 1024*1024*1024), // exactly 1 GiB
			price: model.ResourcePrice{CPUCoreMonth: 0, MemGiBMonth: 100},
			want:  100,
		},
		{
			name:  "millicore conversion",
			spec:  spec(1500, 0), // 1.5 cores
			price: model.ResourcePrice{CPUCoreMonth: 8, MemGiBMonth: 0},
			want:  12,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MonthlyCost(tt.spec, tt.price)
			if !approxEqual(got, tt.want) {
				t.Errorf("MonthlyCost() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMonthlySavings(t *testing.T) {
	price := model.ResourcePrice{CPUCoreMonth: 10, MemGiBMonth: 4}

	tests := []struct {
		name     string
		current  model.ResourceSpec
		proposed model.ResourceSpec
		replicas int32
		want     float64
	}{
		{
			name:     "downsize saves money (positive)",
			current:  spec(1000, bytesPerGiB),  // cost 14
			proposed: spec(500, bytesPerGiB/2), // cost 7
			replicas: 1,
			want:     7,
		},
		{
			name:     "upsize costs more (negative)",
			current:  spec(500, bytesPerGiB/2), // cost 7
			proposed: spec(1000, bytesPerGiB),  // cost 14
			replicas: 1,
			want:     -7,
		},
		{
			name:     "replica scaling multiplies per-replica delta",
			current:  spec(1000, bytesPerGiB),  // 14
			proposed: spec(500, bytesPerGiB/2), // 7
			replicas: 5,
			want:     35,
		},
		{
			name:     "cpu-only change",
			current:  spec(2000, bytesPerGiB), // 20 + 4 = 24
			proposed: spec(1000, bytesPerGiB), // 10 + 4 = 14
			replicas: 3,
			want:     30, // (24-14)*3
		},
		{
			name:     "memory-only change",
			current:  spec(1000, 4*bytesPerGiB), // 10 + 16 = 26
			proposed: spec(1000, bytesPerGiB),   // 10 + 4 = 14
			replicas: 2,
			want:     24, // (26-14)*2
		},
		{
			name:     "zero replicas treated as one",
			current:  spec(1000, bytesPerGiB),  // 14
			proposed: spec(500, bytesPerGiB/2), // 7
			replicas: 0,
			want:     7,
		},
		{
			name:     "negative replicas treated as one",
			current:  spec(1000, bytesPerGiB),
			proposed: spec(500, bytesPerGiB/2),
			replicas: -4,
			want:     7,
		},
		{
			name:     "no change yields zero savings",
			current:  spec(1000, bytesPerGiB),
			proposed: spec(1000, bytesPerGiB),
			replicas: 10,
			want:     0,
		},
		{
			name:     "zero price yields zero savings",
			current:  spec(2000, 4*bytesPerGiB),
			proposed: spec(100, bytesPerGiB),
			replicas: 3,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := price
			if tt.name == "zero price yields zero savings" {
				p = model.ResourcePrice{}
			}
			got := MonthlySavings(tt.current, tt.proposed, p, tt.replicas)
			if !approxEqual(got, tt.want) {
				t.Errorf("MonthlySavings() = %v, want %v", got, tt.want)
			}
		})
	}
}
