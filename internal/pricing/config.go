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
// These are deliberate approximations: ~$24.0 per vCPU-core-month and ~$3.2 per GiB-month,
// roughly a general-purpose vCPU/GiB blended across AWS/GCP/Azure on-demand list pricing as
// of 2025. They give the scan a credible dollar figure without any external cost source
// (Tier 1). When OpenCost is available (Tier 2), opencostProvider replaces these derived
// prices with precise allocated cost. Operators can override via flags.
func DefaultConfig() Config {
	return Config{CPUCoreMonth: 24.0, MemGiBMonth: 3.2}
}

// configProvider is the default PriceProvider: it returns fixed config prices, optionally
// refined by node instance-type labels.
type configProvider struct {
	cfg Config
}

// NewConfigProvider builds the default config-driven PriceProvider.
func NewConfigProvider(cfg Config) Provider { return &configProvider{cfg: cfg} }

func (p *configProvider) Name() string { return "node pricing" }

// instanceTypeLabel is the well-known Kubernetes label that carries a node's cloud
// instance type (e.g. "m5.large"). Both the stable and the deprecated beta forms are
// recognized.
const (
	instanceTypeLabel     = "node.kubernetes.io/instance-type"
	instanceTypeLabelBeta = "beta.kubernetes.io/instance-type"
)

// instanceTypePrices is a tiny, illustrative static map of per-core-month / per-GiB-month
// prices for a handful of common general-purpose instance families. It exists only as an
// optional refinement over the blended config defaults; it is intentionally small and is
// not meant to be exhaustive. Anything not listed falls back to the config prices.
var instanceTypePrices = map[string]model.ResourcePrice{
	// AWS general-purpose (m5/m6i): ~$0.096/hr for 2 vCPU + 8 GiB on-demand.
	"m5.large":  {CPUCoreMonth: 25.0, MemGiBMonth: 3.5},
	"m6i.large": {CPUCoreMonth: 25.0, MemGiBMonth: 3.5},
	// GCP general-purpose (e2-standard-2): cheaper blended.
	"e2-standard-2": {CPUCoreMonth: 20.0, MemGiBMonth: 2.7},
	// Azure general-purpose (Standard_D2s_v3).
	"Standard_D2s_v3": {CPUCoreMonth: 26.0, MemGiBMonth: 3.6},
}

// ResourcePrice returns the configured unit prices, optionally refined by a recognizable
// node instance-type label. It never returns an error: pricing is best-effort and always
// succeeds, falling back to the blended config defaults when the instance type is unknown.
func (p *configProvider) ResourcePrice(ctx context.Context, w model.Workload) (model.ResourcePrice, error) {
	_ = ctx

	if it := instanceType(w.NodeLabels); it != "" {
		if priced, ok := instanceTypePrices[it]; ok {
			priced.Source = "node pricing"
			return priced, nil
		}
	}

	return model.ResourcePrice{
		CPUCoreMonth: p.cfg.CPUCoreMonth,
		MemGiBMonth:  p.cfg.MemGiBMonth,
		Source:       "node pricing",
	}, nil
}

// instanceType extracts a node instance type from scheduling labels, preferring the stable
// label over the deprecated beta one. Returns "" when neither is present.
func instanceType(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	if v := labels[instanceTypeLabel]; v != "" {
		return v
	}
	return labels[instanceTypeLabelBeta]
}
