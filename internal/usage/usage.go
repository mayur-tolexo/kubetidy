// Package usage defines the UsageProvider interface and its implementations for the
// three-tier data ladder. Providers attribute per-container resource usage to workloads.
package usage

import (
	"context"

	"github.com/kubetidy/kubetidy/internal/model"
)

// Provider returns per-container usage statistics for a workload. Implementations
// correspond to evidence tiers (metrics-server, Prometheus, ...).
type Provider interface {
	// Name is a short human-readable identifier (e.g. "metrics-server").
	Name() string
	// Tier reports the evidence tier this provider yields.
	Tier() model.EvidenceTier
	// Usage returns a map keyed by container name. A provider should return an error only
	// for hard failures; missing data for a container is represented by omission.
	Usage(ctx context.Context, w model.Workload) (map[string]model.UsageStats, error)
}
