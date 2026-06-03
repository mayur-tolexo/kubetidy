package pricing

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func ocSvc(name, namespace string, port int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: port}}},
	}
}

func TestDetectOpenCostEndpoint(t *testing.T) {
	client := fake.NewSimpleClientset(ocSvc("opencost", "opencost", 9003))
	ep, ok := DetectOpenCostEndpoint(client)
	if !ok {
		t.Fatal("expected to detect opencost")
	}
	if ep.Namespace != "opencost" || ep.Service != "opencost" || ep.Port != 9003 {
		t.Errorf("endpoint = %+v, want opencost/opencost:9003", ep)
	}
	if ep.InClusterURL() != "http://opencost.opencost.svc:9003" {
		t.Errorf("InClusterURL = %q", ep.InClusterURL())
	}
}

func TestDetectOpenCostEndpoint_None(t *testing.T) {
	if _, ok := DetectOpenCostEndpoint(fake.NewSimpleClientset()); ok {
		t.Error("expected no endpoint on an empty cluster")
	}
	if _, ok := DetectOpenCostEndpoint(nil); ok {
		t.Error("expected no endpoint for a nil client")
	}
}

func TestNewOpenCostProviderViaAPIProxy_NilConfig(t *testing.T) {
	if _, err := NewOpenCostProviderViaAPIProxy(context.Background(), nil, OpenCostEndpoint{}, "7d"); err == nil {
		t.Error("expected an error for a nil rest config")
	}
}

func TestNewOpenCostProviderViaAPIProxy_Unreachable(t *testing.T) {
	cfg := &rest.Config{Host: "https://127.0.0.1:1"} // nothing listening
	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	if _, err := NewOpenCostProviderViaAPIProxy(ctx, cfg,
		OpenCostEndpoint{Namespace: "opencost", Service: "opencost", Port: 9003}, "7d"); err == nil {
		t.Error("expected an error when the OpenCost endpoint does not answer")
	}
}
