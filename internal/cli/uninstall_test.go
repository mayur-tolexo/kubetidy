package cli

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/dynamic"
)

func TestUninstallCommandFlags(t *testing.T) {
	cmd := newUninstallCommand()
	if cmd.Use != "uninstall" {
		t.Errorf("Use = %q, want uninstall", cmd.Use)
	}
	for _, name := range []string{"context", "keep-crds", "yes"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("uninstall command missing --%s flag", name)
		}
	}
}

func TestConfirmUninstall(t *testing.T) {
	orig := confirmReader
	defer func() { confirmReader = orig }()

	cases := map[string]bool{
		"y\n":    true,
		"yes\n":  true,
		"Y\n":    true,
		"YES\n":  true,
		"n\n":    false,
		"\n":     false, // default is No
		"nope\n": false,
		"":       false, // EOF with no input
	}
	for input, want := range cases {
		confirmReader = strings.NewReader(input)
		got, err := confirmUninstall(false)
		if err != nil {
			t.Fatalf("confirmUninstall(%q) error: %v", input, err)
		}
		if got != want {
			t.Errorf("confirmUninstall(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestRunUninstallAbortsOnNo(t *testing.T) {
	orig := confirmReader
	defer func() { confirmReader = orig }()
	confirmReader = strings.NewReader("n\n")

	// With a "no" answer, runUninstall must return before touching any client. If it tried,
	// dynamicFor (real kubeconfig) could error — but it should never get there.
	out := captureStdout(t, func() {
		if err := runUninstall(context.Background(), &uninstallFlags{}); err != nil {
			t.Errorf("runUninstall aborted path error: %v", err)
		}
	})
	if !strings.Contains(out, "Aborted") {
		t.Errorf("expected an Aborted message, got: %s", out)
	}
}

func TestRunUninstallDynamicClientError(t *testing.T) {
	origDyn := dynamicFor
	defer func() { dynamicFor = origDyn }()
	dynamicFor = func(string) (dynamic.Interface, error) { return nil, errTest }

	// --yes skips the prompt and goes straight to building clients, which errors here.
	err := runUninstall(context.Background(), &uninstallFlags{yes: true})
	if err == nil || !strings.Contains(err.Error(), "building dynamic client") {
		t.Fatalf("err = %v, want a building dynamic client error", err)
	}
}
