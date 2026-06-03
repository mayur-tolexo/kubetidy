package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	tea "github.com/charmbracelet/bubbletea"

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
	interactive   bool
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
	flags.BoolVarP(&f.interactive, "interactive", "i", false, "browse recommendations in an interactive TUI")
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
	// Interactive TUI: only when asked, attached to a TTY, in table mode, and with results to
	// browse — otherwise fall through to the normal render (pipes, JSON, CI stay unchanged).
	if f.interactive && f.output != "json" && isTTY(os.Stdout) && len(result.Recommendations) > 0 {
		return runScanTUI(result)
	}
	return render(result, f)
}

// runScanTUI launches the bubbletea browser over the scan result.
var runScanTUI = func(result model.ScanResult) error {
	p := tea.NewProgram(newTUIModel(result), tea.WithAltScreen())
	_, err := p.Run()
	return err
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
			sp.update(scanProgress(done, total))
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

	if p, note, ok := prometheusAutoProvider(clients, f.window); ok {
		*warnings = append(*warnings, fmt.Sprintf("auto-detected Prometheus (%s) — Tier 1", note))
		return p
	} else if note != "" {
		*warnings = append(*warnings, note)
	}

	// No Prometheus: prefer the kubetidy operator's recorded history (Tier 0) when it's
	// installed and has written profiles. Fall back to the metrics-server snapshot per workload
	// so workloads the operator has not profiled yet are still covered (no coverage regression
	// while the operator warms up).
	if usage.DetectOperator(clients.Dynamic) {
		// No note here: the data banner already states the tier, and the per-recommendation
		// evidence line shows which workloads (if any) fell back to a snapshot. A blanket
		// "using operator history" note overclaims while the operator is still warming up.
		return usage.NewFallbackProvider(
			usage.NewOperatorProvider(clients.Dynamic),
			usage.NewMetricsServerProvider(clients.Metrics),
		)
	}

	return usage.NewMetricsServerProvider(clients.Metrics)
}

// prometheusAutoProvider auto-detects an in-cluster Prometheus and returns a Tier-1 provider
// that reaches it through the Kubernetes API server proxy — which works from the user's machine,
// where in-cluster Service DNS does not resolve. It validates reachability (a trivial query)
// before committing, so an unreachable or wrong endpoint falls through to the operator /
// metrics-server instead of silently producing empty, misleading results. The returned note is
// the source label on success, or a fall-back reason when a service was found but unusable
// (empty otherwise). It is a seam so tests can inject a reachable provider without a cluster.
var prometheusAutoProvider = func(clients *kube.Clients, window string) (usage.Provider, string, bool) {
	ep, ok := usage.DetectPrometheusEndpoint(clients.Kube)
	if !ok {
		return nil, "", false
	}
	p, err := usage.NewPrometheusProviderViaAPIProxy(clients.RESTConfig, ep, window)
	if err != nil {
		return nil, fmt.Sprintf("found Prometheus service %s/%s but could not build a client (%v); falling back",
			ep.Namespace, ep.Service, err), false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if !usage.Reachable(ctx, p) {
		return nil, fmt.Sprintf("found Prometheus service %s/%s but it did not answer queries; falling back to operator/metrics-server",
			ep.Namespace, ep.Service), false
	}
	return p, fmt.Sprintf("%s/%s:%d via the API server proxy", ep.Namespace, ep.Service, ep.Port), true
}

// openCostAutoProvider mirrors prometheusAutoProvider for cost: it auto-detects an in-cluster
// OpenCost/Kubecost and returns a Tier-2 provider that reaches it through the API server proxy.
// NewOpenCostProviderViaAPIProxy already queries the allocation API at construction, so a
// non-answering endpoint yields ok=false and we fall back to derived pricing. Seam for tests.
var openCostAutoProvider = func(ctx context.Context, clients *kube.Clients, window string) (pricing.Provider, string, bool) {
	ep, ok := pricing.DetectOpenCostEndpoint(clients.Kube)
	if !ok {
		return nil, "", false
	}
	p, err := pricing.NewOpenCostProviderViaAPIProxy(ctx, clients.RESTConfig, ep, window)
	if err != nil {
		return nil, fmt.Sprintf("found OpenCost service %s/%s but it did not answer (%v); using derived node pricing",
			ep.Namespace, ep.Service, err), false
	}
	return p, fmt.Sprintf("%s/%s:%d via the API server proxy", ep.Namespace, ep.Service, ep.Port), true
}

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

	if p, note, ok := openCostAutoProvider(ctx, clients, f.window); ok {
		*warnings = append(*warnings, fmt.Sprintf("auto-detected OpenCost (%s) — Tier 2 cost", note))
		return p
	} else if note != "" {
		*warnings = append(*warnings, note)
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
