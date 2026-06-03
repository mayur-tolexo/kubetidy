package pricing

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ocCandidate is a well-known OpenCost / Kubecost Service location to probe, in priority order.
type ocCandidate struct {
	namespace string
	service   string
	port      int32
}

// opencostCandidates lists common in-cluster OpenCost (and Kubecost) API service locations,
// most standard first. OpenCost's API listens on 9003; Kubecost's cost-analyzer on 9090.
var opencostCandidates = []ocCandidate{
	{"opencost", "opencost", 9003},
	{"opencost", "opencost-opencost", 9003},
	{"monitoring", "opencost", 9003},
	{"kubecost", "kubecost-cost-analyzer", 9090},
	{"kubecost", "kubecost-cost-analyzer-abc", 9090},
}

// OpenCostEndpoint identifies a detected in-cluster OpenCost/Kubecost Service by coordinates, so
// the caller can reach it either directly (in-cluster) or through the API server proxy (from
// outside the cluster).
type OpenCostEndpoint struct {
	Namespace string
	Service   string
	Port      int32
}

// InClusterURL is the direct, in-cluster API base URL (only resolvable from inside the cluster).
func (e OpenCostEndpoint) InClusterURL() string {
	return fmt.Sprintf("http://%s.%s.svc:%d", e.Service, e.Namespace, e.Port)
}

// DetectOpenCostEndpoint probes the cluster for a well-known OpenCost (or Kubecost) Service and
// returns its coordinates when found. It only confirms the Service exists; the caller validates
// that the API answers. Best-effort, never errors.
func DetectOpenCostEndpoint(client kubernetes.Interface) (OpenCostEndpoint, bool) {
	if client == nil {
		return OpenCostEndpoint{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, c := range opencostCandidates {
		svc, err := client.CoreV1().Services(c.namespace).Get(ctx, c.service, metav1.GetOptions{})
		if err != nil || svc == nil {
			continue
		}
		port := c.port
		if p := firstServicePort(svc); p != 0 {
			port = p
		}
		return OpenCostEndpoint{Namespace: c.namespace, Service: c.service, Port: port}, true
	}
	return OpenCostEndpoint{}, false
}

// DetectOpenCost returns the in-cluster API base URL of a detected OpenCost, or "" when none is
// present. Retained for callers/tests that want the direct URL; new code should prefer
// DetectOpenCostEndpoint so it can route through the API server proxy.
func DetectOpenCost(client kubernetes.Interface) string {
	ep, ok := DetectOpenCostEndpoint(client)
	if !ok {
		return ""
	}
	return ep.InClusterURL()
}

// firstServicePort returns the first declared port of a Service, or 0 if none.
func firstServicePort(svc *corev1.Service) int32 {
	if svc == nil || len(svc.Spec.Ports) == 0 {
		return 0
	}
	return svc.Spec.Ports[0].Port
}
