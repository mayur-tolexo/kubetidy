package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/client-go/dynamic"
)

var errTest = errors.New("injected test error")

func TestInitCommandFlags(t *testing.T) {
	cmd := newInitCommand()
	if cmd.Use != "init" {
		t.Errorf("Use = %q, want init", cmd.Use)
	}
	for _, name := range []string{"context", "crd-only", "print"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("init command missing --%s flag", name)
		}
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
