package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubetidy/kubetidy/internal/cleanup"
	"github.com/kubetidy/kubetidy/internal/kube"
)

type sweepFlags struct {
	kubeContext string
	namespace   string
	storageCost float64
	output      string
}

func newSweepCommand() *cobra.Command {
	f := &sweepFlags{}
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Find removable junk: orphaned Services, unused PVCs, idle namespaces, zombie workloads",
		Long: "sweep scans the cluster (read-only) for cleanup opportunities — Services whose selector " +
			"matches no pods, PersistentVolumeClaims nothing mounts (with an estimated $/mo), namespaces " +
			"with no running workloads, and Deployments/StatefulSets scaled to zero. It never deletes " +
			"anything; it shows you what to review.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSweep(cmd.Context(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.kubeContext, "context", "", "kubeconfig context to use")
	flags.StringVarP(&f.namespace, "namespace", "n", "", "namespace to sweep (default: all; idle-namespace detection needs all)")
	flags.Float64Var(&f.storageCost, "storage-cost", 0.10, "$ per GiB-month for unused-PVC cost estimates")
	flags.StringVarP(&f.output, "output", "o", "table", "output format: table|json")
	return cmd
}

func runSweep(ctx context.Context, f *sweepFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	clients, err := loadClients(f.kubeContext, f.namespace)
	if err != nil {
		return fmt.Errorf("loading kube clients: %w", err)
	}
	findings, err := sweepWithClients(ctx, f, clients)
	if err != nil {
		return err
	}
	return renderSweep(os.Stdout, clients.Context, findings, f.output)
}

// sweepWithClients gathers the live objects and runs the cleanup detectors. Split out from
// runSweep so it can be tested with fake clientsets (no kubeconfig I/O).
func sweepWithClients(ctx context.Context, f *sweepFlags, clients *kube.Clients) ([]cleanup.Finding, error) {
	ns := f.namespace // "" lists across all namespaces
	k := clients.Kube

	svcs, err := k.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("sweep: listing services: %w", err)
	}
	pods, err := k.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("sweep: listing pods: %w", err)
	}
	pvcs, err := k.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("sweep: listing pvcs: %w", err)
	}
	deploys, err := k.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("sweep: listing deployments: %w", err)
	}
	stss, err := k.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("sweep: listing statefulsets: %w", err)
	}
	dss, err := k.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("sweep: listing daemonsets: %w", err)
	}

	in := cleanup.Inputs{
		Services:           svcs.Items,
		Pods:               pods.Items,
		PVCs:               pvcs.Items,
		Deployments:        deploys.Items,
		StatefulSets:       stss.Items,
		DaemonSets:         dss.Items,
		StoragePerGiBMonth: f.storageCost,
	}
	// Idle-namespace detection is cluster-wide; only meaningful when sweeping all namespaces.
	if ns == "" {
		nss, err := k.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("sweep: listing namespaces: %w", err)
		}
		in.Namespaces = nss.Items
	}

	return cleanup.Detect(in), nil
}

// renderSweep writes the findings as a grouped table or JSON.
func renderSweep(w io.Writer, kubeCtx string, findings []cleanup.Finding, output string) error {
	if output == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(findings)
	}
	if output != "" && output != "table" {
		return fmt.Errorf("unknown output format %q (want table|json)", output)
	}

	var b strings.Builder
	ctx := kubeCtx
	if ctx == "" {
		ctx = "(no context)"
	}
	fmt.Fprintf(&b, "kubetidy · %s · cleanup sweep\n\n", ctx)

	if len(findings) == 0 {
		b.WriteString("  ✓ Nothing to clean up — no orphaned, idle, or zombie resources found.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	total := cleanup.TotalMonthlyCost(findings)
	summary := fmt.Sprintf("  Found %s · ", plural(len(findings), "cleanup opportunity", "cleanup opportunities"))
	if total > 0 {
		summary += fmt.Sprintf("~%s/mo reclaimable\n", dollars(total))
	} else {
		summary += "no direct $ estimate (review to reclaim resources)\n"
	}
	b.WriteString(summary)

	// Group by category in the detector's natural order.
	order := []cleanup.Category{cleanup.OrphanedService, cleanup.UnusedPVC, cleanup.IdleNamespace, cleanup.ZombieWorkload}
	byCat := map[cleanup.Category][]cleanup.Finding{}
	for _, f := range findings {
		byCat[f.Category] = append(byCat[f.Category], f)
	}
	for _, cat := range order {
		items := byCat[cat]
		if len(items) == 0 {
			continue
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].MonthlyCost != items[j].MonthlyCost {
				return items[i].MonthlyCost > items[j].MonthlyCost
			}
			return items[i].Namespace+items[i].Name < items[j].Namespace+items[j].Name
		})
		fmt.Fprintf(&b, "\n  %s (%d)\n", cat, len(items))
		for _, f := range items {
			b.WriteString("    • " + sweepLine(f) + "\n")
		}
	}

	b.WriteString("\n  Read-only: review before deleting. kubetidy never deletes anything.\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// sweepLine formats one finding: "ns/Kind name  — reason · detail · ~$X/mo".
func sweepLine(f cleanup.Finding) string {
	loc := f.Name
	if f.Namespace != "" {
		loc = f.Namespace + "/" + f.Name
	}
	// For workloads the kind varies (Deployment vs StatefulSet), so make it explicit.
	if f.Category == cleanup.ZombieWorkload {
		loc += " (" + f.Kind + ")"
	}
	parts := []string{f.Reason}
	if f.Detail != "" {
		parts = append(parts, f.Detail)
	}
	if f.MonthlyCost > 0 {
		parts = append(parts, "~"+dollars(f.MonthlyCost)+"/mo")
	}
	return fmt.Sprintf("%-44s %s", loc, strings.Join(parts, " · "))
}

// dollars formats a whole-dollar estimate, e.g. "$23". Sub-$1 (tiny PVCs) reads "<$1".
func dollars(v float64) string {
	if v > 0 && v < 1 {
		return "<$1"
	}
	return fmt.Sprintf("$%.0f", v)
}

// plural renders "1 thing" / "N things".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
