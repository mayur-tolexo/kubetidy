package pricing

import (
	"context"
	"testing"

	"github.com/kubetidy/kubetidy/internal/model"
)

func TestDefaultConfig(t *testing.T) {
	got := DefaultConfig()
	if got.CPUCoreMonth != 24.0 {
		t.Errorf("CPUCoreMonth = %v, want 24.0", got.CPUCoreMonth)
	}
	if got.MemGiBMonth != 3.2 {
		t.Errorf("MemGiBMonth = %v, want 3.2", got.MemGiBMonth)
	}
}

func TestNewConfigProviderName(t *testing.T) {
	p := NewConfigProvider(DefaultConfig())
	if p.Name() != "node pricing" {
		t.Errorf("Name() = %q, want %q", p.Name(), "node pricing")
	}
}

func TestResourcePrice(t *testing.T) {
	cfg := Config{CPUCoreMonth: 24.0, MemGiBMonth: 3.2}

	tests := []struct {
		name    string
		cfg     Config
		labels  map[string]string
		wantCPU float64
		wantMem float64
		wantSrc string
	}{
		{
			name:    "no labels returns config prices",
			cfg:     cfg,
			labels:  nil,
			wantCPU: 24.0,
			wantMem: 3.2,
			wantSrc: "node pricing",
		},
		{
			name:    "empty labels returns config prices",
			cfg:     cfg,
			labels:  map[string]string{},
			wantCPU: 24.0,
			wantMem: 3.2,
			wantSrc: "node pricing",
		},
		{
			name:    "unknown instance type falls back to config",
			cfg:     cfg,
			labels:  map[string]string{instanceTypeLabel: "totally-made-up"},
			wantCPU: 24.0,
			wantMem: 3.2,
			wantSrc: "node pricing",
		},
		{
			name:    "custom config prices are honored",
			cfg:     Config{CPUCoreMonth: 10.0, MemGiBMonth: 1.0},
			labels:  nil,
			wantCPU: 10.0,
			wantMem: 1.0,
			wantSrc: "node pricing",
		},
		{
			name:    "recognized AWS instance type refines price",
			cfg:     cfg,
			labels:  map[string]string{instanceTypeLabel: "m5.large"},
			wantCPU: 25.0,
			wantMem: 3.5,
			wantSrc: "node pricing",
		},
		{
			name:    "recognized GCP instance type refines price",
			cfg:     cfg,
			labels:  map[string]string{instanceTypeLabel: "e2-standard-2"},
			wantCPU: 20.0,
			wantMem: 2.7,
			wantSrc: "node pricing",
		},
		{
			name:    "beta label is honored",
			cfg:     cfg,
			labels:  map[string]string{instanceTypeLabelBeta: "m6i.large"},
			wantCPU: 25.0,
			wantMem: 3.5,
			wantSrc: "node pricing",
		},
		{
			name: "stable label preferred over beta",
			cfg:  cfg,
			labels: map[string]string{
				instanceTypeLabel:     "e2-standard-2",
				instanceTypeLabelBeta: "m5.large",
			},
			wantCPU: 20.0,
			wantMem: 2.7,
			wantSrc: "node pricing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewConfigProvider(tt.cfg)
			w := model.Workload{NodeLabels: tt.labels}

			got, err := p.ResourcePrice(context.Background(), w)
			if err != nil {
				t.Fatalf("ResourcePrice returned error: %v", err)
			}
			if got.CPUCoreMonth != tt.wantCPU {
				t.Errorf("CPUCoreMonth = %v, want %v", got.CPUCoreMonth, tt.wantCPU)
			}
			if got.MemGiBMonth != tt.wantMem {
				t.Errorf("MemGiBMonth = %v, want %v", got.MemGiBMonth, tt.wantMem)
			}
			if got.Source != tt.wantSrc {
				t.Errorf("Source = %q, want %q", got.Source, tt.wantSrc)
			}
		})
	}
}

func TestResourcePriceNeverErrors(t *testing.T) {
	p := NewConfigProvider(DefaultConfig())
	// Even a fully populated workload with odd labels must succeed in the MVP.
	w := model.Workload{
		Kind:       model.KindDeployment,
		Name:       "x",
		Namespace:  "y",
		NodeLabels: map[string]string{"unrelated": "value"},
	}
	if _, err := p.ResourcePrice(context.Background(), w); err != nil {
		t.Fatalf("ResourcePrice must never error in MVP, got: %v", err)
	}
}
