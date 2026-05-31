package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"

	"github.com/kubetidy/kubetidy/internal/installer"
)

type initFlags struct {
	kubeContext string
	crdOnly     bool
	printOnly   bool
	image       string
}

// discoveryFor is a seam so tests can substitute the discovery client. In production it builds
// one from the same kubeconfig the rest of the CLI uses.
var discoveryFor = func(contextOverride string) (discovery.DiscoveryInterface, error) {
	clients, err := loadClients(contextOverride, "")
	if err != nil {
		return nil, err
	}
	// The kubernetes clientset embeds a discovery client.
	return clients.Kube.Discovery(), nil
}

// dynamicFor is a seam so tests can substitute the dynamic client.
var dynamicFor = func(contextOverride string) (dynamic.Interface, error) {
	clients, err := loadClients(contextOverride, "")
	if err != nil {
		return nil, err
	}
	return clients.Dynamic, nil
}

func newInitCommand() *cobra.Command {
	f := &initFlags{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install kubetidy's in-cluster components (UsageProfile CRD + operator)",
		Long: "init installs everything kubetidy needs in the cluster, from manifests embedded " +
			"in this binary — no separate `kubectl apply -f` required. It applies the UsageProfile " +
			"CRD, waits for it to be established, then deploys the read-only operator that records " +
			"usage history so scans get Prometheus-grade recommendations with no Prometheus.\n\n" +
			"The operator never evicts or resizes workloads; it only observes and records.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd.Context(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.kubeContext, "context", "", "kubeconfig context to use")
	flags.BoolVar(&f.crdOnly, "crd-only", false, "install only the UsageProfile CRD, not the operator")
	flags.BoolVar(&f.printOnly, "print", false, "print the manifests that would be applied, and exit")
	flags.StringVar(&f.image, "image", "", "operator container image to deploy (required on a real cluster; the embedded default is a local kind-only tag)")
	return cmd
}

func runInit(ctx context.Context, f *initFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// --print is offline: emit the embedded manifests for inspection or GitOps, apply nothing.
	if f.printOnly {
		var b strings.Builder
		b.Write(installer.CRDManifest())
		if !f.crdOnly {
			b.WriteString("---\n")
			b.Write(installer.OperatorManifest())
		}
		_, err := io.WriteString(os.Stdout, b.String())
		return err
	}

	dyn, err := dynamicFor(f.kubeContext)
	if err != nil {
		return fmt.Errorf("init: building dynamic client: %w", err)
	}
	disco, err := discoveryFor(f.kubeContext)
	if err != nil {
		return fmt.Errorf("init: building discovery client: %w", err)
	}

	// Warn loudly when installing the operator without a real image: the embedded default is
	// a kind-only tag that will ImagePullBackOff on a real cluster, leaving scans on the
	// limited snapshot tier.
	if !f.crdOnly && f.image == "" {
		_, _ = fmt.Fprintln(os.Stdout,
			"⚠  No --image given: deploying the operator with the local-only dev image, which will\n"+
				"   not pull on a real cluster. Pass --image <registry>/kubetidy-operator:<tag>, or use\n"+
				"   `make operator-deploy` for a local kind cluster.")
	}

	opts := installer.Options{
		IncludeOperator: !f.crdOnly,
		Image:           f.image,
		Log:             func(msg string) { _, _ = fmt.Fprintln(os.Stdout, "•", msg) },
	}
	if err := installer.Install(ctx, dyn, disco, opts); err != nil {
		return err
	}

	_, err = io.WriteString(os.Stdout,
		"\n✓ kubetidy installed. The operator needs a few minutes to accumulate history;\n"+
			"  after that, `kubectl tidy scan` runs at Tier 0 with no Prometheus.\n")
	return err
}
