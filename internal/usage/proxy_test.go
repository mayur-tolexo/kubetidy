package usage

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestDetectPrometheusEndpoint(t *testing.T) {
	client := fake.NewSimpleClientset(svc("prometheus-server", "monitoring", 80))
	ep, ok := DetectPrometheusEndpoint(client)
	if !ok {
		t.Fatal("expected to detect prometheus-server")
	}
	if ep.Namespace != "monitoring" || ep.Service != "prometheus-server" || ep.Port != 80 {
		t.Errorf("endpoint = %+v, want monitoring/prometheus-server:80", ep)
	}
	if ep.InClusterURL() != "http://prometheus-server.monitoring.svc:80" {
		t.Errorf("InClusterURL = %q", ep.InClusterURL())
	}
}

func TestDetectPrometheusEndpoint_None(t *testing.T) {
	if _, ok := DetectPrometheusEndpoint(fake.NewSimpleClientset()); ok {
		t.Error("expected no endpoint on an empty cluster")
	}
	if _, ok := DetectPrometheusEndpoint(nil); ok {
		t.Error("expected no endpoint for a nil client")
	}
}

func TestNewPrometheusProviderViaAPIProxy_NilConfig(t *testing.T) {
	if _, err := NewPrometheusProviderViaAPIProxy(nil, PrometheusEndpoint{}, "14d"); err == nil {
		t.Error("expected an error for a nil rest config")
	}
}

func TestNewPrometheusProviderViaAPIProxy_BadWindow(t *testing.T) {
	cfg := &rest.Config{Host: "https://api.test:6443"}
	if _, err := NewPrometheusProviderViaAPIProxy(cfg, PrometheusEndpoint{Namespace: "monitoring", Service: "prometheus-server", Port: 80}, "nope"); err == nil {
		t.Error("expected an error for an invalid window")
	}
}

func TestNewPrometheusProviderViaAPIProxy_Builds(t *testing.T) {
	cfg := &rest.Config{Host: "https://api.test:6443"}
	p, err := NewPrometheusProviderViaAPIProxy(cfg,
		PrometheusEndpoint{Namespace: "monitoring", Service: "prometheus-server", Port: 80}, "14d")
	if err != nil {
		t.Fatalf("build via proxy: %v", err)
	}
	if p.Name() != "prometheus" {
		t.Errorf("Name = %q, want prometheus", p.Name())
	}
}

func TestReachable_NonPrometheusProvider(t *testing.T) {
	// A non-Prometheus provider can't be probed this way; Reachable must return false, not panic.
	if Reachable(context.Background(), NewMetricsServerProvider(nil)) {
		t.Error("Reachable should be false for a non-Prometheus provider")
	}
}

func TestReachable_UnreachableEndpoint(t *testing.T) {
	cfg := &rest.Config{Host: "https://127.0.0.1:1"} // nothing listening
	p, err := NewPrometheusProviderViaAPIProxy(cfg,
		PrometheusEndpoint{Namespace: "monitoring", Service: "prometheus-server", Port: 80}, "14d")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	if Reachable(ctx, p) {
		t.Error("Reachable should be false when the endpoint does not answer")
	}
}
