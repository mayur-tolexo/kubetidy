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

// DetectOpenCost probes the cluster for a well-known OpenCost (or Kubecost) Service and returns
// its in-cluster API base URL (e.g. "http://opencost.opencost.svc:9003") when found, or ""
// when none is present. Like DetectPrometheus, it only confirms the Service exists; the caller
// (NewOpenCostProvider) validates that the API actually answers. Best-effort, never errors.
func DetectOpenCost(client kubernetes.Interface) string {
	if client == nil {
		return ""
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
		return fmt.Sprintf("http://%s.%s.svc:%d", c.service, c.namespace, port)
	}
	return ""
}

// firstServicePort returns the first declared port of a Service, or 0 if none.
func firstServicePort(svc *corev1.Service) int32 {
	if svc == nil || len(svc.Spec.Ports) == 0 {
		return 0
	}
	return svc.Spec.Ports[0].Port
}
