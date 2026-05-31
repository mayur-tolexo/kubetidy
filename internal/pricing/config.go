package pricing

import (
	"context"

	"github.com/kubetidy/kubetidy/internal/model"
)

// Config holds default unit prices used when no precise cost source (OpenCost) is present.
// Defaults approximate blended on-demand cloud pricing and are overridable via flags.
type Config struct {
	CPUCoreMonth float64
	MemGiBMonth  float64
}

// DefaultConfig returns blended cloud-average defaults.
//
// IMPLEMENTED BY AGENT: tune defaults + document provenance.
func DefaultConfig() Config {
	return Config{CPUCoreMonth: 0, MemGiBMonth: 0}
}

// configProvider is the default PriceProvider: it returns fixed config prices, optionally
// refined by node instance-type labels.
type configProvider struct {
	cfg Config
}

// NewConfigProvider builds the default config-driven PriceProvider.
func NewConfigProvider(cfg Config) Provider { return &configProvider{cfg: cfg} }

func (p *configProvider) Name() string { return "node pricing" }

// ResourcePrice returns the configured unit prices.
//
// IMPLEMENTED BY AGENT.
func (p *configProvider) ResourcePrice(ctx context.Context, w model.Workload) (model.ResourcePrice, error) {
	_ = ctx
	_ = w
	return model.ResourcePrice{}, nil
}
