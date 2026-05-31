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
	window        string
	cpuCoreMonth  float64
	memGiBMonth   float64
}

func newScanCommand() *cobra.Command {
	f := &scanFlags{}
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan the cluster and report efficiency, waste, and rightsizing recommendations",
		RunE: func(cmd *cobra.Command, args []string) error {
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
	flags.StringVar(&f.window, "window", "14d", "Prometheus lookback window")
	flags.Float64Var(&f.cpuCoreMonth, "cpu-cost", 0, "override $ per CPU core-month (0 = default)")
	flags.Float64Var(&f.memGiBMonth, "mem-cost", 0, "override $ per GiB-month (0 = default)")
	return cmd
}

// runScan resolves clients, selects the best usage tier, runs the engine, and renders.
//
// The wiring here is intentionally thin; the heavy lifting lives in the bounded packages.
func runScan(ctx context.Context, f *scanFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}

	clients, err := kube.Load(f.kubeContext, f.namespace)
	if err != nil {
		return fmt.Errorf("loading kube clients: %w", err)
	}

	workloads, err := kube.Discover(ctx, clients, f.namespace)
	if err != nil {
		return fmt.Errorf("discovering workloads: %w", err)
	}

	var warnings []string
	usageProvider := selectUsageProvider(clients, f, &warnings)

	cfg := pricing.DefaultConfig()
	if f.cpuCoreMonth > 0 {
		cfg.CPUCoreMonth = f.cpuCoreMonth
	}
	if f.memGiBMonth > 0 {
		cfg.MemGiBMonth = f.memGiBMonth
	}
	priceProvider := pricing.NewConfigProvider(cfg)

	engine := &scan.Engine{
		Workloads: workloads,
		Usage:     usageProvider,
		Price:     priceProvider,
		Policy:    model.DefaultPolicy(),
		Context:   clients.Context,
		Namespace: f.namespace,
	}
	result, err := engine.Run(ctx)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	result.Warnings = append(warnings, result.Warnings...)

	return render(result, f)
}

// selectUsageProvider picks Tier 1 (Prometheus) when a URL is provided, otherwise Tier 0
// (metrics-server). Degradations are appended to warnings.
//
// IMPLEMENTED BY AGENT (auto-detection of Prometheus is a Phase-1 enhancement): for the MVP
// this honors --prometheus-url, else falls back to metrics-server.
func selectUsageProvider(clients *kube.Clients, f *scanFlags, warnings *[]string) usage.Provider {
	if f.prometheusURL != "" {
		p, err := usage.NewPrometheusProvider(f.prometheusURL, f.window)
		if err == nil {
			return p
		}
		*warnings = append(*warnings, fmt.Sprintf("Prometheus unavailable (%v); using metrics-server", err))
	}
	return usage.NewMetricsServerProvider(clients.Metrics)
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
