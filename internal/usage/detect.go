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

// DetectPrometheus probes the cluster for a well-known Prometheus Service and returns its
// in-cluster base URL (e.g. "http://prometheus-server.monitoring.svc:80") when found, or ""
// when none is present. It only confirms the Service exists; the caller validates that the
// endpoint actually answers queries. It is best-effort and never errors.
func DetectPrometheus(client kubernetes.Interface) string {
	if client == nil {
		return ""
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
