package cli

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kfake "k8s.io/client-go/kubernetes/fake"
	mfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/kubetidy/kubetidy/internal/kube"
	"github.com/kubetidy/kubetidy/internal/model"
)

func orchInt32(v int32) *int32 { return &v }

// orchFakeClients builds a *kube.Clients backed by fake clientsets seeded with one
// over-provisioned Deployment, so the scan orchestration runs hermetically (no kubeconfig).
func orchFakeClients(t *testing.T) *kube.Clients {
	t.Helper()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop"},
		Spec: appsv1.DeploymentSpec{
			Replicas: orchInt32(1),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "api",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2000m"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				}}},
			},
		},
	}
	return &kube.Clients{
		Kube:      kfake.NewSimpleClientset(dep),
		Metrics:   mfake.NewSimpleClientset(),
		Context:   "test-ctx",
		Namespace: "shop",
	}
}

func TestRunEngineWithClients(t *testing.T) {
	result, err := runEngineWithClients(context.Background(), &scanFlags{namespace: "shop", window: "14d"}, orchFakeClients(t), nil)
	if err != nil {
		t.Fatalf("runEngineWithClients error: %v", err)
	}
	if result.Context != "test-ctx" {
		t.Errorf("Context = %q, want test-ctx", result.Context)
	}
	if result.WorkloadCount != 1 {
		t.Errorf("WorkloadCount = %d, want 1", result.WorkloadCount)
	}
}

func TestRunEngineLoaderError(t *testing.T) {
	orig := loadClients
	defer func() { loadClients = orig }()
	loadClients = func(_, _ string) (*kube.Clients, error) { return nil, context.DeadlineExceeded }

	_, err := runEngine(context.Background(), &scanFlags{})
	if err == nil || !strings.Contains(err.Error(), "loading kube clients") {
		t.Fatalf("err = %v, want loading kube clients error", err)
	}
}

func TestRunEngineUsesInjectedLoader(t *testing.T) {
	orig := loadClients
	defer func() { loadClients = orig }()
	clients := orchFakeClients(t)
	loadClients = func(_, _ string) (*kube.Clients, error) { return clients, nil }

	result, err := runEngine(context.Background(), &scanFlags{namespace: "shop", window: "14d"})
	if err != nil {
		t.Fatalf("runEngine error: %v", err)
	}
	if result.WorkloadCount != 1 {
		t.Errorf("WorkloadCount = %d, want 1", result.WorkloadCount)
	}
}

func TestRunEngineDiscoverError(t *testing.T) {
	origLoad := loadClients
	origDisc := discoverWorkloads
	defer func() { loadClients = origLoad; discoverWorkloads = origDisc }()
	loadClients = func(_, _ string) (*kube.Clients, error) { return orchFakeClients(t), nil }
	discoverWorkloads = func(_ context.Context, _ *kube.Clients, _ string) ([]model.Workload, error) {
		return nil, context.DeadlineExceeded
	}
	_, err := runEngine(context.Background(), &scanFlags{window: "14d"})
	if err == nil || !strings.Contains(err.Error(), "discovering workloads") {
		t.Fatalf("err = %v, want discovering workloads error", err)
	}
}

func TestSelectUsageProviderExplicitPrometheus(t *testing.T) {
	var warnings []string
	p := selectUsageProvider(orchFakeClients(t), &scanFlags{prometheusURL: "http://prom:9090", window: "14d"}, &warnings)
	if p.Name() != "prometheus" {
		t.Errorf("provider = %q, want prometheus", p.Name())
	}
}

func TestSelectUsageProviderBadPrometheusFallsBack(t *testing.T) {
	var warnings []string
	p := selectUsageProvider(orchFakeClients(t), &scanFlags{prometheusURL: "http://prom:9090", window: "bad-window"}, &warnings)
	if p.Name() != "metrics-server" {
		t.Errorf("provider = %q, want metrics-server fallback", p.Name())
	}
	if len(warnings) == 0 {
		t.Error("expected a fallback warning")
	}
}

func TestSelectUsageProviderAutoDetect(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "prometheus-server", Namespace: "monitoring"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}},
	}
	clients := &kube.Clients{Kube: kfake.NewSimpleClientset(svc), Metrics: mfake.NewSimpleClientset()}
	var warnings []string
	p := selectUsageProvider(clients, &scanFlags{window: "14d"}, &warnings)
	if p.Name() != "prometheus" {
		t.Errorf("provider = %q, want auto-detected prometheus", p.Name())
	}
	if !strings.Contains(strings.Join(warnings, " "), "auto-detected") {
		t.Errorf("warnings = %v, want an auto-detected note", warnings)
	}
}

func TestSelectUsageProviderDefaultMetricsServer(t *testing.T) {
	var warnings []string
	p := selectUsageProvider(orchFakeClients(t), &scanFlags{window: "14d"}, &warnings)
	if p.Name() != "metrics-server" {
		t.Errorf("provider = %q, want metrics-server", p.Name())
	}
}

func TestRunScanInjected(t *testing.T) {
	orig := loadClients
	defer func() { loadClients = orig }()
	loadClients = func(_, _ string) (*kube.Clients, error) { return orchFakeClients(t), nil }

	out := captureStdout(t, func() {
		if err := runScan(context.Background(), &scanFlags{namespace: "shop", output: "json", window: "14d"}); err != nil {
			t.Errorf("runScan error: %v", err)
		}
	})
	if !strings.Contains(strings.ToLower(out), "efficiencyscore") {
		t.Errorf("json output missing score field:\n%s", out)
	}
}

func TestRunDiffInjected(t *testing.T) {
	orig := loadClients
	defer func() { loadClients = orig }()
	loadClients = func(_, _ string) (*kube.Clients, error) { return orchFakeClients(t), nil }

	out := captureStdout(t, func() {
		if err := runDiff(context.Background(), &scanFlags{namespace: "shop", window: "14d"}); err != nil {
			t.Errorf("runDiff error: %v", err)
		}
	})
	if out == "" {
		t.Error("expected some diff output")
	}
}

func TestRunPRInjectedEmpty(t *testing.T) {
	orig := loadClients
	defer func() { loadClients = orig }()
	loadClients = func(_, _ string) (*kube.Clients, error) { return orchFakeClients(t), nil }

	dir := t.TempDir()
	out := captureStdout(t, func() {
		f := &prFlags{scanFlags: scanFlags{namespace: "shop", window: "14d"}, outDir: dir + "/patches"}
		if err := runPR(context.Background(), f); err != nil {
			t.Errorf("runPR error: %v", err)
		}
	})
	if !strings.Contains(out, "No rightsizing recommendations") && !strings.Contains(out, "Wrote") {
		t.Errorf("unexpected pr output:\n%s", out)
	}
}
