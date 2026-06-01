// Package pricing defines the price Provider interface and two implementations behind it: a
// default config-driven one (derived blended cloud rates, Tier 1) and an OpenCost-backed one
// that serves precise allocated cost when OpenCost is present (Tier 2).
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
