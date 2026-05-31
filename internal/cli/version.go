package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/kubetidy/kubetidy/internal/version"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			_, _ = fmt.Fprintln(os.Stdout, version.String())
		},
	}
}

// isTTY reports whether w is an interactive terminal (for color/bars).
func isTTY(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
