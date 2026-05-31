package cli

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kubetidy/kubetidy/internal/model"
	"github.com/kubetidy/kubetidy/internal/patch"
)

func newDiffCommand() *cobra.Command {
	f := &scanFlags{}
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show the exact, reversible `kubectl patch` to apply each rightsizing recommendation",
		Long: "diff scans the cluster (read-only) and, for each rightsizing recommendation, prints the " +
			"exact strategic-merge `kubectl patch` command that would apply it, along with the monthly " +
			"savings. kubetidy never runs these — you review, run, or discard them.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDiff(cmd.Context(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&f.namespace, "namespace", "n", "", "namespace to scan (default: all)")
	flags.StringVar(&f.kubeContext, "context", "", "kubeconfig context to use")
	flags.StringVar(&f.explain, "explain", "", "only show the patch for a single workload")
	flags.IntVar(&f.topN, "top", 20, "max patches to show (0 = all)")
	flags.StringVar(&f.prometheusURL, "prometheus-url", "", "Prometheus base URL (forces Tier 1)")
	flags.StringVar(&f.window, "window", "14d", "Prometheus lookback window")
	flags.Float64Var(&f.cpuCoreMonth, "cpu-cost", 0, "override $ per CPU core-month (0 = default)")
	flags.Float64Var(&f.memGiBMonth, "mem-cost", 0, "override $ per GiB-month (0 = default)")
	return cmd
}

func runDiff(ctx context.Context, f *scanFlags) error {
	result, err := runEngine(ctx, f)
	if err != nil {
		return err
	}
	return renderDiff(os.Stdout, result, f.explain, f.topN)
}

// renderDiff writes, for each recommendation (sorted by savings desc, filtered by `explain`,
// limited by topN), a header and the reversible kubectl patch command.
func renderDiff(w io.Writer, result model.ScanResult, explain string, topN int) error {
	recs := make([]model.Recommendation, len(result.Recommendations))
	copy(recs, result.Recommendations)
	sort.SliceStable(recs, func(i, j int) bool {
		return recs[i].MonthlySavings > recs[j].MonthlySavings
	})

	var b strings.Builder
	count := 0
	for _, rec := range recs {
		if explain != "" && !matchesWorkload(rec, explain) {
			continue
		}
		cmd, err := patch.KubectlCommand(rec)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "# %s (%s) · saves %s/mo · conf %d%%\n",
			rec.Workload.Name, rec.Workload.Ref(), signedDollars(rec.MonthlySavings), rec.Confidence.Percent())
		b.WriteString(cmd)
		b.WriteString("\n\n")
		count++
		if topN > 0 && count >= topN {
			break
		}
	}
	if count == 0 {
		b.WriteString("No rightsizing recommendations.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// matchesWorkload reports whether the query matches the recommendation's container name or
// workload name/ref.
func matchesWorkload(rec model.Recommendation, query string) bool {
	return strings.Contains(rec.ContainerName, query) ||
		strings.Contains(rec.Workload.Name, query) ||
		strings.Contains(rec.Workload.Ref(), query)
}

// signedDollars renders a whole-dollar amount keeping its sign (a negative-savings "grow"
// recommendation reads "-$X").
func signedDollars(v float64) string {
	return fmt.Sprintf("$%.0f", math.Round(v))
}
