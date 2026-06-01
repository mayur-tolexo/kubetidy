package operator

import (
	"context"
	"log"
	"time"
)

// Options configures the operator run loop.
type Options struct {
	// ScrapeInterval is how often the operator samples metrics-server and folds the readings
	// into its histograms. Defaults to 30s.
	ScrapeInterval time.Duration
	// Logger receives one-line operational messages. Defaults to the standard logger.
	Logger *log.Logger
}

// withDefaults fills unset Options fields with safe defaults.
func (o Options) withDefaults() Options {
	if o.ScrapeInterval <= 0 {
		o.ScrapeInterval = 30 * time.Second
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	return o
}

// Run drives the collector on a ticker until ctx is cancelled. It runs one Tick immediately so
// the operator starts recording without waiting a full interval, then ticks on ScrapeInterval.
// Per-tick errors are logged but never stop the loop — the operator must keep running.
func Run(ctx context.Context, c *Collector, opts Options) error {
	opts = opts.withDefaults()
	opts.Logger.Printf("kubetidy operator starting; scrape interval %s", opts.ScrapeInterval)

	tick := func() {
		if err := c.Tick(ctx); err != nil {
			opts.Logger.Printf("tick error (continuing): %v", err)
		}
	}
	tick()

	ticker := time.NewTicker(opts.ScrapeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			opts.Logger.Printf("kubetidy operator shutting down: %v", ctx.Err())
			return ctx.Err()
		case <-ticker.C:
			tick()
		}
	}
}
