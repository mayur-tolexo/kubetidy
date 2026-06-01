package pricing

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func svc(name, namespace string, ports ...int32) *corev1.Service {
	s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	for _, p := range ports {
		s.Spec.Ports = append(s.Spec.Ports, corev1.ServicePort{Port: p})
	}
	return s
}

func TestDetectOpenCost_NilClient(t *testing.T) {
	if got := DetectOpenCost(nil); got != "" {
		t.Fatalf("DetectOpenCost(nil) = %q, want empty", got)
	}
}

func TestDetectOpenCost_EmptyCluster(t *testing.T) {
	if got := DetectOpenCost(fake.NewSimpleClientset()); got != "" {
		t.Fatalf("DetectOpenCost(empty) = %q, want empty", got)
	}
}

func TestDetectOpenCost(t *testing.T) {
	tests := []struct {
		name     string
		services []*corev1.Service
		want     string
	}{
		{
			name:     "standard opencost in opencost namespace",
			services: []*corev1.Service{svc("opencost", "opencost", 9003)},
			want:     "http://opencost.opencost.svc:9003",
		},
		{
			name:     "kubecost cost-analyzer",
			services: []*corev1.Service{svc("kubecost-cost-analyzer", "kubecost", 9090)},
			want:     "http://kubecost-cost-analyzer.kubecost.svc:9090",
		},
		{
			name:     "declared port overrides candidate default",
			services: []*corev1.Service{svc("opencost", "opencost", 8080)},
			want:     "http://opencost.opencost.svc:8080",
		},
		{
			name:     "no service yields empty",
			services: nil,
			want:     "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := make([]runtime.Object, 0, len(tc.services))
			for _, s := range tc.services {
				objs = append(objs, s)
			}
			client := fake.NewSimpleClientset(objs...)
			if got := DetectOpenCost(client); got != tc.want {
				t.Errorf("DetectOpenCost = %q, want %q", got, tc.want)
			}
		})
	}
}
