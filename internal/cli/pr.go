package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kubetidy/kubetidy/internal/gitops"
)

type prFlags struct {
	scanFlags
	outDir      string
	includeGrow bool
	bodyOut     string
}

func newPRCommand() *cobra.Command {
	f := &prFlags{}
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Generate a reviewable GitOps change set (patches + PR body) from recommendations",
		Long: "pr scans the cluster (read-only) and writes one strategic-merge patch file per " +
			"rightsizing recommendation plus a Markdown pull-request body that leads with the " +
			"monthly dollar savings. kubetidy never commits, pushes, or applies anything — you " +
			"review the files, open the PR, and let your GitOps controller (or kubectl) do the rest.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPR(cmd.Context(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&f.namespace, "namespace", "n", "", "namespace to scan (default: all)")
	flags.StringVar(&f.kubeContext, "context", "", "kubeconfig context to use")
	flags.IntVar(&f.topN, "top", 0, "max recommendations to include (0 = all)")
	flags.StringVar(&f.prometheusURL, "prometheus-url", "", "Prometheus base URL (forces Tier 1)")
	flags.StringVar(&f.window, "window", "14d", "Prometheus lookback window")
	flags.Float64Var(&f.cpuCoreMonth, "cpu-cost", 0, "override $ per CPU core-month (0 = default)")
	flags.Float64Var(&f.memGiBMonth, "mem-cost", 0, "override $ per GiB-month (0 = default)")
	flags.StringVar(&f.outDir, "out", "kubetidy-patches", "directory to write patch files into")
	flags.BoolVar(&f.includeGrow, "include-grow", false, "also include 'grow' (under-provisioned) recommendations")
	flags.StringVar(&f.bodyOut, "body-out", "", "write the PR body to this file (default: stdout)")
	return cmd
}

func runPR(ctx context.Context, f *prFlags) error {
	result, err := runEngine(ctx, &f.scanFlags)
	if err != nil {
		return err
	}

	cs, err := gitops.Build(result, gitops.Options{
		PatchDir:    filepath.Base(f.outDir),
		TopN:        f.topN,
		IncludeGrow: f.includeGrow,
	})
	if err != nil {
		return err
	}

	if cs.Count == 0 {
		_, err := io.WriteString(os.Stdout, "No rightsizing recommendations — nothing to generate.\n")
		return err
	}

	// Write patch files (under the user-chosen --out directory).
	if err := os.MkdirAll(f.outDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", f.outDir, err)
	}
	for _, file := range cs.Files {
		dest := filepath.Join(f.outDir, filepath.Base(file.Path))
		if err := os.WriteFile(dest, file.Contents, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
	}

	// Write the PR body to a file when asked.
	if f.bodyOut != "" {
		if err := os.WriteFile(f.bodyOut, []byte(cs.PRBody), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", f.bodyOut, err)
		}
	}

	// Build the summary in one buffer and write it once (no unchecked Fprint errors).
	var b strings.Builder
	fmt.Fprintf(&b, "Wrote %d patch file(s) to %s/\n", cs.Count, f.outDir)
	fmt.Fprintf(&b, "PR title: %s\n", cs.PRTitle)
	if f.bodyOut != "" {
		fmt.Fprintf(&b, "PR body:  %s\n", f.bodyOut)
	} else {
		b.WriteString("\n--- PR body ---\n")
		b.WriteString(cs.PRBody)
		b.WriteString("\n")
	}
	_, err = io.WriteString(os.Stdout, b.String())
	return err
}
