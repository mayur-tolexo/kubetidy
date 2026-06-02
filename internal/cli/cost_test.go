package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const baseDep = `apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec: {replicas: 2, template: {spec: {containers: [{name: app, resources: {requests: {cpu: "500m", memory: "512Mi"}}}]}}}
`
const headDep = `apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec: {replicas: 4, template: {spec: {containers: [{name: app, resources: {requests: {cpu: "1", memory: "1Gi"}}}]}}}
`

func runCostCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newCostCommand()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestCostDiffAndGuardrail(t *testing.T) {
	base := t.TempDir()
	head := t.TempDir()
	writeManifest(t, base, "app.yaml", baseDep)
	writeManifest(t, head, "app.yaml", headDep)

	out, err := runCostCmd(t, "--base", base, "--head", head)
	if err != nil {
		t.Fatalf("cost: %v", err)
	}
	if !strings.Contains(out, "this change adds") || !strings.Contains(out, "Deployment/shop/api") {
		t.Errorf("unexpected cost output:\n%s", out)
	}

	// Guardrail: a tiny budget should fail.
	_, err = runCostCmd(t, "--base", base, "--head", head, "--fail-over", "1")
	if err == nil || !strings.Contains(err.Error(), "guardrail") {
		t.Errorf("expected guardrail failure, got %v", err)
	}

	// A generous budget should pass.
	if _, err := runCostCmd(t, "--base", base, "--head", head, "--fail-over", "10000"); err != nil {
		t.Errorf("generous budget should pass, got %v", err)
	}
}

func TestCostSingleSetTotal(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "app.yaml", headDep)
	out, err := runCostCmd(t, dir)
	if err != nil {
		t.Fatalf("cost: %v", err)
	}
	if !strings.Contains(out, "Total:") || !strings.Contains(out, "Deployment/shop/api") {
		t.Errorf("unexpected single-set output:\n%s", out)
	}
}

func TestCostNoManifests(t *testing.T) {
	cmd := newCostCommand()
	cmd.SetArgs([]string{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when no manifests provided")
	}
}
