// Command kubetidy is the kubetidy CLI. The same binary is installed as both `kubetidy`
// (standalone) and `kubectl-tidy` (so `kubectl tidy ...` works via the kubectl plugin
// convention). The root command adapts its displayed name to os.Args[0].
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kubetidy/kubetidy/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCommand(os.Args[0])
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
