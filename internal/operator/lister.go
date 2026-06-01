package operator

import (
	"context"

	"github.com/kubetidy/kubetidy/internal/kube"
	"github.com/kubetidy/kubetidy/internal/model"
)

// kubeLister is the production WorkloadLister: it discovers all Deployments/StatefulSets/
// DaemonSets in the cluster via the shared kube discovery code, so the operator profiles the
// same workloads the CLI scans.
type kubeLister struct {
	clients *kube.Clients
}

// NewKubeLister builds a WorkloadLister over all namespaces.
func NewKubeLister(clients *kube.Clients) WorkloadLister {
	return &kubeLister{clients: clients}
}

// List returns every workload in the cluster (all namespaces).
func (l *kubeLister) List(ctx context.Context) ([]model.Workload, error) {
	return kube.Discover(ctx, l.clients, "")
}
