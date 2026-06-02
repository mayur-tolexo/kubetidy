package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
)

var errTest = errors.New("injected test error")

func TestInitCommandFlags(t *testing.T) {
	cmd := newInitCommand()
	if cmd.Use != "init" {
		t.Errorf("Use = %q, want init", cmd.Use)
	}
	for _, name := range []string{"context", "crd-only", "print", "image", "with-opencost", "prometheus-url"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("init command missing --%s flag", name)
		}
	}
}

func TestRunInitPrintWithOpenCost(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runInit(context.Background(), &initFlags{
			printOnly:     true,
			withOpenCost:  true,
			prometheusURL: "http://prom.observability.svc:9090",
		}); err != nil {
			t.Errorf("runInit --print --with-opencost error: %v", err)
		}
	})
	if !strings.Contains(out, "kubetidy-operator") {
		t.Error("print output missing the operator")
	}
	if !strings.Contains(out, "opencost") {
		t.Error("print output missing OpenCost manifest")
	}
	if !strings.Contains(out, "http://prom.observability.svc:9090") {
		t.Error("print output missing the substituted Prometheus URL")
	}
}

func TestRunInitPrintNoOpenCostByDefault(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runInit(context.Background(), &initFlags{printOnly: true}); err != nil {
			t.Errorf("runInit --print error: %v", err)
		}
	})
	if strings.Contains(out, "PROMETHEUS_SERVER_ENDPOINT") {
		t.Error("OpenCost manifest should not be printed without --with-opencost")
	}
}

func TestRunInitPrintFull(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runInit(context.Background(), &initFlags{printOnly: true}); err != nil {
			t.Errorf("runInit --print error: %v", err)
		}
	})
	if !strings.Contains(out, "usageprofiles.kubetidy.io") {
		t.Error("print output missing the CRD")
	}
	if !strings.Contains(out, "kubetidy-operator") {
		t.Error("print output missing the operator (full install)")
	}
	if !strings.Contains(out, "---") {
		t.Error("print output missing the document separator between CRD and operator")
	}
}

func TestRunInitPrintCRDOnly(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runInit(context.Background(), &initFlags{printOnly: true, crdOnly: true}); err != nil {
			t.Errorf("runInit --print --crd-only error: %v", err)
		}
	})
	if !strings.Contains(out, "usageprofiles.kubetidy.io") {
		t.Error("crd-only print output missing the CRD")
	}
	if strings.Contains(out, "kubetidy-operator") {
		t.Error("crd-only print output should NOT include the operator deployment")
	}
}

func TestRunInitDynamicClientError(t *testing.T) {
	origDyn := dynamicFor
	defer func() { dynamicFor = origDyn }()
	dynamicFor = func(string) (dynamic.Interface, error) { return nil, errTest }

	err := runInit(context.Background(), &initFlags{})
	if err == nil || !strings.Contains(err.Error(), "building dynamic client") {
		t.Fatalf("err = %v, want a building dynamic client error", err)
	}
}

func TestRunInitDiscoveryClientError(t *testing.T) {
	origDyn := dynamicFor
	origDisco := discoveryFor
	defer func() { dynamicFor = origDyn; discoveryFor = origDisco }()

	dynamicFor = func(string) (dynamic.Interface, error) {
		return dynfake.NewSimpleDynamicClient(runtime.NewScheme()), nil
	}
	discoveryFor = func(string) (discovery.DiscoveryInterface, error) { return nil, errTest }

	err := runInit(context.Background(), &initFlags{})
	if err == nil || !strings.Contains(err.Error(), "building discovery client") {
		t.Fatalf("err = %v, want a building discovery client error", err)
	}
}
