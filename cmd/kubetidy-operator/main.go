// Command kubetidy-operator is the in-cluster usage historian. It periodically samples
// metrics-server, accumulates per-container usage into decaying histograms, and checkpoints the
// result into UsageProfile custom resources so `kubectl tidy scan` can produce Prometheus-grade
// recommendations with no Prometheus. It is strictly read-only with respect to workloads: it
// observes and records, and never evicts or resizes anything. See docs/design/operator.md.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kubetidy/kubetidy/internal/histogram"
	"github.com/kubetidy/kubetidy/internal/kube"
	"github.com/kubetidy/kubetidy/internal/operator"
	"github.com/kubetidy/kubetidy/internal/version"
)

func main() {
	var (
		scrapeInterval = flag.Duration("scrape-interval", 30*time.Second,
			"how often to sample metrics-server and fold readings into usage history")
		cpuHalfLife = flag.Duration("cpu-half-life", histogram.DefaultHalfLife,
			"decay half-life for CPU usage history (longer remembers longer cycles; same footprint)")
		memHalfLife = flag.Duration("memory-half-life", histogram.DefaultHalfLife,
			"decay half-life for memory usage history")
	)
	flag.Parse()

	logger := log.New(os.Stdout, "kubetidy-operator ", log.LstdFlags|log.LUTC)
	logger.Printf("version %s", version.String())
	logger.Printf("scrape-interval=%s cpu-half-life=%s memory-half-life=%s",
		*scrapeInterval, *cpuHalfLife, *memHalfLife)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// In-cluster (or local kubeconfig) clients; the empty context/namespace overrides mean
	// "use the active context, all namespaces".
	clients, err := kube.Load("", "")
	if err != nil {
		logger.Fatalf("loading kube clients: %v", err)
	}

	lister := operator.NewKubeLister(clients)
	collector := operator.NewCollector(
		lister,
		operator.NewMetricsSampler(clients.Metrics),
		operator.NewDynamicStore(clients.Dynamic),
		time.Now,
	).WithHistogramConfig(
		histogram.DefaultCPUConfig().WithHalfLife(*cpuHalfLife),
		histogram.DefaultMemoryConfig().WithHalfLife(*memHalfLife),
	)

	// Resume from any previously checkpointed history so a restart does not cold-start.
	if workloads, err := lister.List(ctx); err != nil {
		logger.Printf("rehydrate: listing workloads failed (starting fresh): %v", err)
	} else {
		collector.Rehydrate(ctx, workloads)
		logger.Printf("rehydrated history for up to %d workloads", len(workloads))
	}

	if err := operator.Run(ctx, collector, operator.Options{ScrapeInterval: *scrapeInterval, Logger: logger}); err != nil && ctx.Err() == nil {
		logger.Fatalf("operator run failed: %v", err)
	}
}
