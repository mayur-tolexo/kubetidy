package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kubetidy/kubetidy/internal/installer"
)

type uninstallFlags struct {
	kubeContext  string
	keepCRDs     bool
	withOpenCost bool
	yes          bool
	dryRun       bool
}

// confirmReader is the input used for the interactive confirmation prompt; overridable in
// tests.
var confirmReader io.Reader = os.Stdin

func newUninstallCommand() *cobra.Command {
	f := &uninstallFlags{}
	cmd := &cobra.Command{
		Use:     "uninstall",
		Aliases: []string{"cleanup"},
		Short:   "Remove kubetidy's in-cluster components (operator + CRDs) — the inverse of init",
		Long: "uninstall (alias: cleanup) deletes everything `kubectl tidy init` created: the " +
			"operator (Deployment, RBAC, namespace) and the kubetidy CRDs. Deleting the CRDs also " +
			"removes all recorded usage history (UsageProfile, ClusterUsageSummary, Recommendation).\n\n" +
			"Use --dry-run to list what would be removed without deleting anything. Use --keep-crds " +
			"to remove only the operator and preserve the CRDs and their data (e.g. before " +
			"redeploying). It is idempotent: already-absent objects are skipped.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(cmd.Context(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.kubeContext, "context", "", "kubeconfig context to use")
	flags.BoolVar(&f.keepCRDs, "keep-crds", false, "remove only the operator; keep the CRDs and recorded data")
	flags.BoolVar(&f.withOpenCost, "with-opencost", false, "also remove the OpenCost deployment kubetidy installed with init --with-opencost")
	flags.BoolVar(&f.dryRun, "dry-run", false, "list what would be removed without deleting anything")
	flags.BoolVarP(&f.yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func runUninstall(ctx context.Context, f *uninstallFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// A real (non-dry-run) uninstall is destructive, so confirm first unless --yes.
	if !f.dryRun && !f.yes {
		ok, err := confirmUninstall(f.keepCRDs)
		if err != nil {
			return err
		}
		if !ok {
			_, err := io.WriteString(os.Stdout, "Aborted.\n")
			return err
		}
	}

	dyn, err := dynamicFor(f.kubeContext)
	if err != nil {
		return fmt.Errorf("uninstall: building dynamic client: %w", err)
	}
	disco, err := discoveryFor(f.kubeContext)
	if err != nil {
		return fmt.Errorf("uninstall: building discovery client: %w", err)
	}

	logf := func(msg string) { _, _ = fmt.Fprintln(os.Stdout, "•", msg) }
	if err := installer.Uninstall(ctx, dyn, disco, installer.UninstallOptions{
		KeepCRDs:        f.keepCRDs,
		IncludeOpenCost: f.withOpenCost,
		DryRun:          f.dryRun,
		Log:             logf,
	}); err != nil {
		return err
	}

	if f.dryRun {
		_, err = io.WriteString(os.Stdout, "\nDry run — nothing was deleted. Re-run without --dry-run to apply.\n")
	} else {
		_, err = io.WriteString(os.Stdout, "\n✓ kubetidy removed from the cluster.\n")
	}
	return err
}

// confirmUninstall prompts for y/N confirmation, returning true only on an explicit yes. The
// message warns about data loss unless CRDs are kept.
func confirmUninstall(keepCRDs bool) (bool, error) {
	warning := "This deletes the kubetidy operator AND all CRDs, including every recorded\n" +
		"UsageProfile/ClusterUsageSummary/Recommendation (usage history will be lost)."
	if keepCRDs {
		warning = "This deletes the kubetidy operator. CRDs and recorded data are kept."
	}
	_, _ = fmt.Fprintf(os.Stdout, "%s\nProceed? [y/N]: ", warning)

	reader := bufio.NewReader(confirmReader)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("uninstall: reading confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
