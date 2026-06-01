package usage

import (
	"context"

	"github.com/kubetidy/kubetidy/internal/model"
)

// fallbackProvider chains two providers: it serves each workload from the primary, falling back
// to the secondary for workloads the primary has no data for. This lets the scan use the
// kubetidy operator's recorded history (Tier 0) where it exists while still covering
// not-yet-profiled workloads from the metrics-server snapshot — so installing the operator never
// reduces coverage during its warm-up.
//
// Tier() reports the primary's tier (the headline data source). Per-container UsageStats already
// carry their own Tier, so a fallen-back workload is still scored at the snapshot's lower
// confidence even though the headline tier is the primary's.
type fallbackProvider struct {
	primary   Provider
	secondary Provider
}

// NewFallbackProvider builds a provider that prefers primary per workload and uses secondary
// when primary returns no data (or errors) for that workload.
func NewFallbackProvider(primary, secondary Provider) Provider {
	return &fallbackProvider{primary: primary, secondary: secondary}
}

func (p *fallbackProvider) Name() string             { return p.primary.Name() }
func (p *fallbackProvider) Tier() model.EvidenceTier { return p.primary.Tier() }

// Usage returns the primary's stats for the workload when it has any, otherwise the secondary's.
// A primary error is treated as "no data" and falls back, so a transient primary failure never
// fails a workload the secondary can still cover.
func (p *fallbackProvider) Usage(ctx context.Context, w model.Workload) (map[string]model.UsageStats, error) {
	stats, err := p.primary.Usage(ctx, w)
	if err == nil && len(stats) > 0 {
		return stats, nil
	}
	return p.secondary.Usage(ctx, w)
}
