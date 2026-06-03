package usage

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// candidate is a well-known Prometheus Service location to probe, in priority order. These
// cover the kube-prometheus-stack, the Prometheus community Helm chart, and bare installs.
type candidate struct {
	namespace string
	service   string
	port      int32
}

// prometheusCandidates lists the common in-cluster Prometheus service locations, most
// specific / most common first.
var prometheusCandidates = []candidate{
	{"monitoring", "prometheus-kube-prometheus-prometheus", 9090},
	{"monitoring", "prometheus-operated", 9090},
	{"monitoring", "prometheus-server", 80},
	{"monitoring", "prometheus", 9090},
	{"prometheus", "prometheus-server", 80},
	{"prometheus", "prometheus-operated", 9090},
	{"kube-system", "prometheus", 9090},
	{"default", "prometheus-server", 80},
}

// PrometheusEndpoint identifies a detected in-cluster Prometheus Service by coordinates, so the
// caller can reach it either directly (in-cluster) or through the API server proxy (from
// outside the cluster).
type PrometheusEndpoint struct {
	Namespace string
	Service   string
	Port      int32
}

// InClusterURL is the direct, in-cluster base URL (only resolvable from inside the cluster).
func (e PrometheusEndpoint) InClusterURL() string {
	return fmt.Sprintf("http://%s.%s.svc:%d", e.Service, e.Namespace, e.Port)
}

// DetectPrometheusEndpoint probes the cluster for a well-known Prometheus Service and returns
// its coordinates when found. It only confirms the Service exists; the caller validates that the
// endpoint actually answers queries. It is best-effort and never errors.
func DetectPrometheusEndpoint(client kubernetes.Interface) (PrometheusEndpoint, bool) {
	if client == nil {
		return PrometheusEndpoint{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, c := range prometheusCandidates {
		svc, err := client.CoreV1().Services(c.namespace).Get(ctx, c.service, metav1.GetOptions{})
		if err != nil || svc == nil {
			continue
		}
		port := c.port
		if p := firstServicePort(svc); p != 0 {
			port = p
		}
		return PrometheusEndpoint{Namespace: c.namespace, Service: c.service, Port: port}, true
	}
	return PrometheusEndpoint{}, false
}

// DetectPrometheus returns the in-cluster base URL of a detected Prometheus, or "" when none is
// present. Retained for callers/tests that want the direct URL; new code should prefer
// DetectPrometheusEndpoint so it can route through the API server proxy.
func DetectPrometheus(client kubernetes.Interface) string {
	ep, ok := DetectPrometheusEndpoint(client)
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
