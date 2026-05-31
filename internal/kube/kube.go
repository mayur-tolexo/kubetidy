// Package kube handles client-go setup (kubeconfig/context resolution, like any kubectl
// plugin) and workload discovery.
package kube

import (
	"context"

	"k8s.io/client-go/kubernetes"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/kubetidy/kubetidy/internal/model"
)

// Clients bundles the typed clients kubetidy uses.
type Clients struct {
	Kube      kubernetes.Interface
	Metrics   metricsv.Interface
	Context   string
	Namespace string
}

// Load builds Clients from the standard kubeconfig loading rules (KUBECONFIG, --context,
// in-cluster fallback). namespaceOverride, when non-empty, scopes discovery.
//
// IMPLEMENTED BY AGENT: see internal/kube task. Use clientcmd loading rules so the binary
// behaves like a kubectl plugin and inherits the active context/namespace.
func Load(contextOverride, namespaceOverride string) (*Clients, error) {
	return nil, nil
}

// Discover lists Deployments, StatefulSets and DaemonSets in the given namespace (empty =
// all namespaces) and converts them to model.Workload, normalizing resource quantities to
// millicores and bytes.
//
// IMPLEMENTED BY AGENT.
func Discover(ctx context.Context, c *Clients, namespace string) ([]model.Workload, error) {
	_ = ctx
	_ = c
	_ = namespace
	return nil, nil
}
