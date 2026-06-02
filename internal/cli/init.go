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
	kubeContext    string
	crdOnly        bool
	printOnly      bool
	image          string
	withOpenCost   bool
	withPrometheus bool
	prometheusURL  string
}

// includePrometheus reports whether init should deploy the bundled Prometheus: when asked
// explicitly, or implicitly when OpenCost is requested without an external Prometheus to use
// (OpenCost can't run without one).
func (f *initFlags) includePrometheus() bool {
	return f.withPrometheus || (f.withOpenCost && f.prometheusURL == "")
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
	flags.BoolVar(&f.withOpenCost, "with-opencost", false, "also deploy OpenCost for precise Tier-2 cost (auto-deploys a bundled Prometheus unless --prometheus-url is given)")
	flags.BoolVar(&f.withPrometheus, "with-prometheus", false, "also deploy a minimal Prometheus (monitoring namespace) so Tier-1 scans work with no external Prometheus")
	flags.StringVar(&f.prometheusURL, "prometheus-url", "", "use this existing Prometheus endpoint for OpenCost instead of the bundled one (default http://prometheus-server.monitoring.svc:80)")
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
		if f.includePrometheus() {
			b.WriteString("---\n")
			b.Write(installer.PrometheusManifest())
		}
		if f.withOpenCost {
			b.WriteString("---\n")
			b.Write(installer.OpenCostManifest(f.prometheusURL))
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

	opts := installer.Options{
		IncludeOperator:   !f.crdOnly,
		IncludeOpenCost:   f.withOpenCost,
		IncludePrometheus: f.includePrometheus(),
		PrometheusURL:     f.prometheusURL,
		Image:             f.image,
		Log:               func(msg string) { _, _ = fmt.Fprintln(os.Stdout, "•", msg) },
	}
	if err := installer.Install(ctx, dyn, disco, opts); err != nil {
		return err
	}

	msg := "\n✓ kubetidy installed. The operator needs a few minutes to accumulate history;\n" +
		"  after that, `kubectl tidy scan` runs at Tier 0 with no Prometheus.\n"
	if f.includePrometheus() {
		msg += "  A bundled Prometheus is deploying in the monitoring namespace (scrapes\n" +
			"  kubelet/cAdvisor); it unlocks Tier-1 history and feeds OpenCost.\n"
	}
	if f.withOpenCost {
		msg += "  OpenCost is deploying in the opencost namespace; once both it and Prometheus\n" +
			"  are ready, scans show precise Tier-2 cost from your cluster's node pricing.\n"
	}
	_, err = io.WriteString(os.Stdout, msg)
	return err
}
