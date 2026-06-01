package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kubetidy/kubetidy/internal/kube"
	"github.com/kubetidy/kubetidy/internal/model"
	"github.com/kubetidy/kubetidy/internal/pricing"
	"github.com/kubetidy/kubetidy/internal/report"
	"github.com/kubetidy/kubetidy/internal/scan"
	"github.com/kubetidy/kubetidy/internal/usage"
)

type scanFlags struct {
	namespace     string
	kubeContext   string
	output        string
	explain       string
	topN          int
	prometheusURL string
	opencostURL   string
	window        string
	cpuCoreMonth  float64
	memGiBMonth   float64
}

// clientLoader resolves Kubernetes clients from kubeconfig. It is a seam: production uses
// kube.Load, tests inject a fake so the orchestration in runEngineWithClients is hermetic.
type clientLoader func(contextOverride, namespaceOverride string) (*kube.Clients, error)

// loadClients is the production loader, overridable in tests.
var loadClients clientLoader = kube.Load

// discoverWorkloads is the production discovery function, overridable in tests.
var discoverWorkloads = kube.Discover

func newScanCommand() *cobra.Command {
	f := &scanFlags{}
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan the cluster and report efficiency, waste, and rightsizing recommendations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScan(cmd.Context(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&f.namespace, "namespace", "n", "", "namespace to scan (default: all)")
	flags.StringVar(&f.kubeContext, "context", "", "kubeconfig context to use")
	flags.StringVarP(&f.output, "output", "o", "table", "output format: table|json")
	flags.StringVar(&f.explain, "explain", "", "show full derivation for a single workload")
	flags.IntVar(&f.topN, "top", 20, "max recommendations to show (0 = all)")
	flags.StringVar(&f.prometheusURL, "prometheus-url", "", "Prometheus base URL (forces Tier 1)")
	flags.StringVar(&f.opencostURL, "opencost-url", "", "OpenCost base URL for precise cost (forces Tier 2; auto-detected otherwise)")
	flags.StringVar(&f.window, "window", "14d", "Prometheus lookback window")
	flags.Float64Var(&f.cpuCoreMonth, "cpu-cost", 0, "override $ per CPU core-month (0 = default)")
	flags.Float64Var(&f.memGiBMonth, "mem-cost", 0, "override $ per GiB-month (0 = default)")
	return cmd
}

// runScan resolves clients, runs the engine, and renders the report.
func runScan(ctx context.Context, f *scanFlags) error {
	result, err := runEngine(ctx, f)
	if err != nil {
		return err
	}
	return render(result, f)
}

// runEngine resolves clients (with a live progress spinner) then runs the testable
// orchestration. It is shared by the `scan`, `diff`, and `pr` commands so all three see
// identical recommendations.
func runEngine(ctx context.Context, f *scanFlags) (model.ScanResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	sp := newSpinner("connecting to cluster…")
	sp.start()
	defer sp.finish()

	clients, err := loadClients(f.kubeContext, f.namespace)
	if err != nil {
		return model.ScanResult{}, fmt.Errorf("loading kube clients: %w", err)
	}
	sp.update("discovering workloads…")
	return runEngineWithClients(ctx, f, clients, sp)
}

// runEngineWithClients holds the testable orchestration: given resolved clients, discover
// workloads, choose the usage tier, price, and run the scan engine. No kubeconfig I/O, so it
// is exercised hermetically with fake clientsets. sp may be nil (no progress reporting).
func runEngineWithClients(ctx context.Context, f *scanFlags, clients *kube.Clients, sp *spinner) (model.ScanResult, error) {
	workloads, err := discoverWorkloads(ctx, clients, f.namespace)
	if err != nil {
		return model.ScanResult{}, fmt.Errorf("discovering workloads: %w", err)
	}

	var warnings []string
	usageProvider := selectUsageProvider(clients, f, &warnings)

	priceProvider := selectPriceProvider(ctx, clients, f, &warnings)

	engine := &scan.Engine{
		Workloads: workloads,
		Usage:     usageProvider,
		Price:     priceProvider,
		Policy:    model.DefaultPolicy(),
		Context:   clients.Context,
		Namespace: f.namespace,
	}
	if sp != nil {
		engine.Progress = func(done, total int) {
			sp.update(fmt.Sprintf("analyzing workloads… %d/%d", done, total))
		}
	}
	result, err := engine.Run(ctx)
	if err != nil {
		return model.ScanResult{}, fmt.Errorf("scan: %w", err)
	}
	result.Warnings = append(warnings, result.Warnings...)

	return result, nil
}

// selectUsageProvider picks the best available usage tier:
//   - an explicit --prometheus-url forces Tier 1;
//   - otherwise kubetidy auto-detects an in-cluster Prometheus (the common kube-prometheus /
//     Helm service names) and uses it when reachable, upgrading the scan to Tier 1 with no
//     configuration — this is what gets a user off low-confidence Tier 0 automatically;
//   - failing that, it falls back to Tier 0 (metrics-server).
//
// Every fallback is recorded in warnings so the report explains why a given tier was used.
func selectUsageProvider(clients *kube.Clients, f *scanFlags, warnings *[]string) usage.Provider {
	if f.prometheusURL != "" {
		p, err := usage.NewPrometheusProvider(f.prometheusURL, f.window)
		if err == nil {
			return p
		}
		*warnings = append(*warnings, fmt.Sprintf("Prometheus unavailable (%v); using metrics-server", err))
		return usage.NewMetricsServerProvider(clients.Metrics)
	}

	if url := usage.DetectPrometheus(clients.Kube); url != "" {
		if p, err := usage.NewPrometheusProvider(url, f.window); err == nil {
			*warnings = append(*warnings, fmt.Sprintf("auto-detected Prometheus at %s (Tier 1)", url))
			return p
		}
	}

	// No Prometheus: prefer the kubetidy operator's recorded history (Tier 0) when it's
	// installed and has written profiles. Fall back to the metrics-server snapshot per workload
	// so workloads the operator has not profiled yet are still covered (no coverage regression
	// while the operator warms up).
	if usage.DetectOperator(clients.Dynamic) {
		*warnings = append(*warnings, "using kubetidy operator history (Tier 0); metrics-server snapshot covers any not-yet-profiled workloads")
		return usage.NewFallbackProvider(
			usage.NewOperatorProvider(clients.Dynamic),
			usage.NewMetricsServerProvider(clients.Metrics),
		)
	}

	return usage.NewMetricsServerProvider(clients.Metrics)
}

// detectOpenCost is a seam over pricing.DetectOpenCost so tests can point auto-detection at a
// reachable endpoint (the real detector returns an in-cluster URL that a unit test can't reach).
var detectOpenCost = pricing.DetectOpenCost

// selectPriceProvider picks the cost source, mirroring selectUsageProvider:
//   - an explicit --opencost-url forces Tier 2 (precise allocated cost);
//   - otherwise kubetidy auto-detects an in-cluster OpenCost/Kubecost service and uses it when
//     its allocation API answers, upgrading cost to Tier 2 with no configuration;
//   - failing that, it falls back to derived node pricing (configProvider), honoring the
//     --cpu-cost / --mem-cost overrides.
//
// Every upgrade or failed attempt is recorded in warnings so the report explains the source.
func selectPriceProvider(ctx context.Context, clients *kube.Clients, f *scanFlags, warnings *[]string) pricing.Provider {
	cfg := pricing.DefaultConfig()
	if f.cpuCoreMonth > 0 {
		cfg.CPUCoreMonth = f.cpuCoreMonth
	}
	if f.memGiBMonth > 0 {
		cfg.MemGiBMonth = f.memGiBMonth
	}
	fallback := pricing.NewConfigProvider(cfg)

	if f.opencostURL != "" {
		p, err := pricing.NewOpenCostProvider(ctx, f.opencostURL, f.window)
		if err == nil {
			*warnings = append(*warnings, fmt.Sprintf("using OpenCost at %s for cost (Tier 2)", f.opencostURL))
			return p
		}
		*warnings = append(*warnings, fmt.Sprintf("OpenCost unavailable (%v); using derived node pricing", err))
		return fallback
	}

	if url := detectOpenCost(clients.Kube); url != "" {
		if p, err := pricing.NewOpenCostProvider(ctx, url, f.window); err == nil {
			*warnings = append(*warnings, fmt.Sprintf("auto-detected OpenCost at %s (Tier 2 cost)", url))
			return p
		}
	}
	return fallback
}

func render(result model.ScanResult, f *scanFlags) error {
	switch f.output {
	case "json":
		return report.JSON(os.Stdout, result)
	case "table", "":
		opts := report.Options{Color: isTTY(os.Stdout), TopN: f.topN, Explain: f.explain}
		return report.Table(os.Stdout, result, opts)
	default:
		return fmt.Errorf("unknown output format %q (want table|json)", f.output)
	}
}
