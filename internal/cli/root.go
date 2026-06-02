// Package cli builds the cobra command tree. It is shared by both binary faces
// (kubetidy and kubectl-tidy); the root command name adapts to os.Args[0].
package cli

import (
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kubetidy/kubetidy/internal/version"
)

// NewRootCommand builds the root command. invokedAs is os.Args[0]; when the binary is run as
// a kubectl plugin (kubectl-tidy), the user-facing name becomes "kubectl tidy".
func NewRootCommand(invokedAs string) *cobra.Command {
	use := rootUse(invokedAs)
	root := &cobra.Command{
		Use:           use,
		Short:         "See your cluster's wasted dollars — and what to safely rightsize.",
		Long:          "kubetidy scores cluster efficiency, quantifies wasted spend in dollars, and produces evidence-backed, action-ready rightsizing recommendations.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	root.AddCommand(newScanCommand())
	root.AddCommand(newDiffCommand())
	root.AddCommand(newSweepCommand())
	root.AddCommand(newPRCommand())
	root.AddCommand(newInitCommand())
	root.AddCommand(newUninstallCommand())
	root.AddCommand(newVersionCommand())
	return root
}

// rootUse derives the displayed command name from the invocation path.
func rootUse(invokedAs string) string {
	base := filepath.Base(invokedAs)
	if strings.HasPrefix(base, "kubectl-") {
		return "kubectl " + strings.TrimPrefix(base, "kubectl-")
	}
	if base == "" || base == "." {
		return "kubetidy"
	}
	return base
}
