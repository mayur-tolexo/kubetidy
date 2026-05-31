// Package pricing defines the PriceProvider interface and a default, config-driven
// implementation. Tier 2 (OpenCost) will add a second implementation behind this interface.
package pricing

import (
	"context"

	"github.com/kubetidy/kubetidy/internal/model"
)

// Provider returns the unit price attributable to a workload's scheduling target.
type Provider interface {
	// Name is a short human-readable identifier (e.g. "node pricing").
	Name() string
	// ResourcePrice returns per-core-month and per-GiB-month prices for the workload.
	ResourcePrice(ctx context.Context, w model.Workload) (model.ResourcePrice, error)
}
