package usage

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// svc builds a corev1.Service with the given name/namespace and optional ports.
func svc(name, namespace string, ports ...int32) *corev1.Service {
	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	for _, p := range ports {
		s.Spec.Ports = append(s.Spec.Ports, corev1.ServicePort{Port: p})
	}
	return s
}

func TestDetectPrometheus_NilClient(t *testing.T) {
	if got := DetectPrometheus(nil); got != "" {
		t.Fatalf("DetectPrometheus(nil) = %q, want empty string", got)
	}
}

func TestDetectPrometheus_EmptyCluster(t *testing.T) {
	client := fake.NewSimpleClientset()
	if got := DetectPrometheus(client); got != "" {
		t.Fatalf("DetectPrometheus(empty) = %q, want empty string", got)
	}
}

func TestDetectPrometheus(t *testing.T) {
	tests := []struct {
		name     string
		services []*corev1.Service
		want     string
	}{
		{
			name:     "no services",
			services: nil,
			want:     "",
		},
		{
			name: "prometheus-server in monitoring with declared port 80",
			services: []*corev1.Service{
				svc("prometheus-server", "monitoring", 80),
			},
			want: "http://prometheus-server.monitoring.svc:80",
		},
		{
			name: "declared port overrides candidate default port",
			// prometheus-operated's candidate default port is 9090, but the
			// Service declares 9091, so the declared port must win.
			services: []*corev1.Service{
				svc("prometheus-operated", "monitoring", 9091),
			},
			want: "http://prometheus-operated.monitoring.svc:9091",
		},
		{
			name: "higher-priority candidate wins over lower one",
			// prometheus-kube-prometheus-prometheus is first in the candidate
			// list, so it must win over prometheus-server which is later.
			services: []*corev1.Service{
				svc("prometheus-server", "monitoring", 80),
				svc("prometheus-kube-prometheus-prometheus", "monitoring", 9090),
			},
			want: "http://prometheus-kube-prometheus-prometheus.monitoring.svc:9090",
		},
		{
			name: "no ports falls back to candidate default port",
			// prometheus-server has no declared ports, so firstServicePort
			// returns 0 and the candidate default (80) is used.
			services: []*corev1.Service{
				svc("prometheus-server", "monitoring"),
			},
			want: "http://prometheus-server.monitoring.svc:80",
		},
		{
			name: "candidate in non-monitoring namespace",
			services: []*corev1.Service{
				svc("prometheus", "kube-system", 9090),
			},
			want: "http://prometheus.kube-system.svc:9090",
		},
		{
			name: "service with non-matching name is ignored",
			services: []*corev1.Service{
				svc("grafana", "monitoring", 3000),
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := make([]runtime.Object, 0, len(tt.services))
			for _, s := range tt.services {
				objs = append(objs, s)
			}
			client := fake.NewSimpleClientset(objs...)
			got := DetectPrometheus(client)
			if got != tt.want {
				t.Fatalf("DetectPrometheus() = %q, want %q", got, tt.want)
			}
			// Sanity: when a URL is returned it must contain the expected
			// service, namespace and port substrings.
			if tt.want != "" {
				for _, sub := range []string{"http://", ".svc:"} {
					if !strings.Contains(got, sub) {
						t.Errorf("DetectPrometheus() = %q, missing substring %q", got, sub)
					}
				}
			}
		})
	}
}
