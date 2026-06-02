package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kubetidy/kubetidy/internal/manifest"
	"github.com/kubetidy/kubetidy/internal/model"
)

type costFlags struct {
	base         string
	head         string
	cpuCoreMonth float64
	memGiBMonth  float64
	failOver     float64
	failOverSet  bool
	output       string
}

func newCostCommand() *cobra.Command {
	f := &costFlags{}
	cmd := &cobra.Command{
		Use:   "cost [manifests...]",
		Short: "Estimate the $/mo of resource requests in manifests; diff a PR's before/after",
		Long: "cost prices the CPU/memory requests in Kubernetes manifests (no cluster needed) and, " +
			"given --base and --head, reports the monthly $ a change adds or saves — the CI cost-guardrail " +
			"(\"this PR adds $400/mo\"). Use --fail-over to fail CI when the net increase exceeds a budget.",
		RunE: func(cmd *cobra.Command, args []string) error {
			f.failOverSet = cmd.Flags().Changed("fail-over")
			return runCost(cmd, f, args)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.base, "base", "", "base manifest file or dir (the 'before' — e.g. main branch)")
	flags.StringVar(&f.head, "head", "", "head manifest file or dir (the 'after' — e.g. the PR). Defaults to positional args")
	flags.Float64Var(&f.cpuCoreMonth, "cpu-cost", 0, "override $ per CPU core-month (0 = default)")
	flags.Float64Var(&f.memGiBMonth, "mem-cost", 0, "override $ per GiB-month (0 = default)")
	flags.Float64Var(&f.failOver, "fail-over", 0, "exit non-zero if the net monthly increase exceeds this $ amount")
	flags.StringVarP(&f.output, "output", "o", "table", "output format: table|json")
	return cmd
}

func runCost(cmd *cobra.Command, f *costFlags, args []string) error {
	price := manifest.DefaultPrice(f.cpuCoreMonth, f.memGiBMonth)

	// Head = --head, or positional args (so `kubetidy cost deploy.yaml` works).
	headPaths := args
	if f.head != "" {
		headPaths = append([]string{f.head}, args...)
	}
	if len(headPaths) == 0 {
		return fmt.Errorf("cost: provide manifests as arguments or via --head")
	}
	headWls, err := loadManifestPaths(headPaths)
	if err != nil {
		return err
	}
	headCost := manifest.CostWorkloads(headWls, price)

	var baseCost []manifest.WorkloadCost
	if f.base != "" {
		baseWls, err := loadManifestPaths([]string{f.base})
		if err != nil {
			return err
		}
		baseCost = manifest.CostWorkloads(baseWls, price)
	}

	report := manifest.Compare(baseCost, headCost)
	if err := renderCost(cmd.OutOrStdout(), report, f.base != "", f.output); err != nil {
		return err
	}

	// CI guardrail: fail when the net increase exceeds the budget.
	if f.failOverSet && report.NetDelta > f.failOver {
		return fmt.Errorf("cost guardrail: net increase %s/mo exceeds budget %s/mo",
			signedDollars(report.NetDelta), signedDollars(f.failOver))
	}
	return nil
}

// loadManifestPaths reads every .yaml/.yml/.json under the given files/dirs and parses workloads.
func loadManifestPaths(paths []string) ([]model.Workload, error) {
	var all []model.Workload
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("cost: %w", err)
		}
		var files []string
		if info.IsDir() {
			entries, err := os.ReadDir(p)
			if err != nil {
				return nil, fmt.Errorf("cost: reading dir %s: %w", p, err)
			}
			for _, e := range entries {
				if !e.IsDir() && isManifestFile(e.Name()) {
					files = append(files, filepath.Join(p, e.Name()))
				}
			}
		} else {
			files = []string{p}
		}
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err != nil {
				return nil, fmt.Errorf("cost: reading %s: %w", file, err)
			}
			wls, err := manifest.ParseWorkloadsBytes(b)
			if err != nil {
				return nil, fmt.Errorf("cost: parsing %s: %w", file, err)
			}
			all = append(all, wls...)
		}
	}
	return all, nil
}

func isManifestFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func renderCost(w io.Writer, rep manifest.CostReport, diff bool, output string) error {
	if output == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if output != "" && output != "table" {
		return fmt.Errorf("unknown output format %q (want table|json)", output)
	}

	var b strings.Builder
	if !diff {
		// Single set: just the total monthly cost.
		fmt.Fprintf(&b, "kubetidy cost\n\n  Total: %s/mo (resource requests)\n\n", signedDollars(rep.AfterTotal))
		for _, c := range rep.Changes {
			fmt.Fprintf(&b, "  %-44s %s/mo\n", c.Ref, signedDollars(c.After))
		}
		_, err := io.WriteString(w, b.String())
		return err
	}

	verb := "adds"
	if rep.NetDelta < 0 {
		verb = "saves"
	}
	headline := fmt.Sprintf("this change %s %s/mo", verb, signedDollars(abs64f(rep.NetDelta)))
	if rep.NetDelta == 0 {
		headline = "no monthly cost change"
	}
	fmt.Fprintf(&b, "kubetidy cost · base → head\n\n  %s  (%s/mo → %s/mo)\n\n",
		headline, signedDollars(rep.BeforeTotal), signedDollars(rep.AfterTotal))

	fmt.Fprintf(&b, "  %-40s %10s %10s %10s\n", "WORKLOAD", "BEFORE", "AFTER", "DELTA")
	for _, c := range rep.Changes {
		if c.Status == manifest.Unchanged {
			continue
		}
		fmt.Fprintf(&b, "  %-40s %10s %10s %10s  (%s)\n",
			truncate(c.Ref, 40), dollarsOrDash(c.Before, c.Status == manifest.Added),
			dollarsOrDash(c.After, c.Status == manifest.Removed), signedDelta(c.Delta), c.Status)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func dollarsOrDash(v float64, dash bool) string {
	if dash {
		return "—"
	}
	return signedDollars(v)
}

func signedDelta(v float64) string {
	if v > 0 {
		return "+" + signedDollars(v)
	}
	if v < 0 {
		return "-" + signedDollars(abs64f(v))
	}
	return "$0"
}

func abs64f(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
