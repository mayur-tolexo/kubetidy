// Command kubetidy is the kubetidy CLI. The same binary is installed as both `kubetidy`
// (standalone) and `kubectl-tidy` (so `kubectl tidy ...` works via the kubectl plugin
// convention). The root command adapts its displayed name to os.Args[0].
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/klog/v2"

	"github.com/kubetidy/kubetidy/internal/cli"
)

func main() {
	// client-go logs through klog (e.g. rate-limit warnings). This is a clean end-user CLI, so
	// keep those internal logs out of the output entirely.
	klog.LogToStderr(false)
	klog.SetOutput(io.Discard)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCommand(os.Args[0])
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
