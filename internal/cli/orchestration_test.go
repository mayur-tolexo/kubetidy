package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	mfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
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

// --- price provider selection (Tier 1 derived vs Tier 2 OpenCost) ----------------------------

func TestSelectPriceProviderDefaultDerived(t *testing.T) {
	var warnings []string
	p := selectPriceProvider(context.Background(), orchFakeClients(t), &scanFlags{window: "14d"}, &warnings)
	if p.Name() != "node pricing" {
		t.Errorf("provider = %q, want node pricing (Tier 1 default)", p.Name())
	}
}

func TestSelectPriceProviderExplicitOpenCost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":[{"shop/api":{"name":"shop/api",` +
			`"properties":{"namespace":"shop","controller":"api"},` +
			`"cpuCoreHours":10,"cpuCost":0.5,"ramByteHours":1073741824000,"ramCost":0.2}}]}`))
	}))
	defer srv.Close()

	var warnings []string
	p := selectPriceProvider(context.Background(), orchFakeClients(t),
		&scanFlags{opencostURL: srv.URL, window: "7d"}, &warnings)
	if p.Name() != "OpenCost" {
		t.Errorf("provider = %q, want OpenCost", p.Name())
	}
	if !strings.Contains(strings.Join(warnings, " "), "OpenCost") {
		t.Errorf("warnings = %v, want an OpenCost note", warnings)
	}
}

func TestSelectPriceProviderBadOpenCostFallsBack(t *testing.T) {
	var warnings []string
	p := selectPriceProvider(context.Background(), orchFakeClients(t),
		&scanFlags{opencostURL: "http://opencost.invalid:9003", window: "7d"}, &warnings)
	if p.Name() != "node pricing" {
		t.Errorf("provider = %q, want node pricing fallback", p.Name())
	}
	if !strings.Contains(strings.Join(warnings, " "), "unavailable") {
		t.Errorf("warnings = %v, want an unavailable note", warnings)
	}
}

func TestSelectPriceProviderAutoDetect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":[{"shop/api":{"name":"shop/api",` +
			`"properties":{"namespace":"shop","controller":"api"},` +
			`"cpuCoreHours":10,"cpuCost":0.5,"ramByteHours":1073741824000,"ramCost":0.2}}]}`))
	}))
	defer srv.Close()

	// Override the detection seam so auto-detect resolves to the reachable test server.
	origDetect := detectOpenCost
	defer func() { detectOpenCost = origDetect }()
	detectOpenCost = func(kubernetes.Interface) string { return srv.URL }

	var warnings []string
	p := selectPriceProvider(context.Background(), orchFakeClients(t), &scanFlags{window: "7d"}, &warnings)
	if p.Name() != "OpenCost" {
		t.Errorf("provider = %q, want auto-detected OpenCost", p.Name())
	}
	if !strings.Contains(strings.Join(warnings, " "), "auto-detected OpenCost") {
		t.Errorf("warnings = %v, want auto-detected OpenCost note", warnings)
	}
}

// --- operator (Tier 0) usage selection ------------------------------------------------------

func TestSelectUsageProviderPrefersOperatorWhenProfilesExist(t *testing.T) {
	// A dynamic client seeded with one UsageProfile makes DetectOperator return true.
	scheme := runtime.NewScheme()
	gvr := usageprofile.GroupVersionResource()
	listKinds := map[schema.GroupVersionResource]string{gvr: "UsageProfileList"}
	prof := usageprofile.UsageProfile{
		Namespace: "shop",
		Name:      usageprofile.ObjectName("Deployment", "api"),
		Status:    usageprofile.Status{SampleCount: 100},
	}
	u := prof.ToUnstructured()
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: usageprofile.Group, Version: usageprofile.Version, Kind: usageprofile.Kind})
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, u)

	clients := &kube.Clients{
		Kube:    kfake.NewSimpleClientset(), // no Prometheus service
		Metrics: mfake.NewSimpleClientset(),
		Dynamic: dyn,
	}
	var warnings []string
	p := selectUsageProvider(clients, &scanFlags{window: "14d"}, &warnings)
	if p.Name() != "kubetidy operator" {
		t.Errorf("provider = %q, want kubetidy operator (Tier 0)", p.Name())
	}
	// No blanket note is emitted for the operator: the data banner states the tier and the
	// per-recommendation evidence shows any snapshot fallback, so a "using operator" note would
	// overclaim during warm-up.
	if strings.Contains(strings.Join(warnings, " "), "operator") {
		t.Errorf("did not expect an operator note, got %v", warnings)
	}
}

func TestSelectUsageProviderNoOperatorFallsBackToSnapshot(t *testing.T) {
	// No UsageProfiles (empty dynamic client) -> DetectOperator false -> metrics-server.
	scheme := runtime.NewScheme()
	gvr := usageprofile.GroupVersionResource()
	listKinds := map[schema.GroupVersionResource]string{gvr: "UsageProfileList"}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)

	clients := &kube.Clients{Kube: kfake.NewSimpleClientset(), Metrics: mfake.NewSimpleClientset(), Dynamic: dyn}
	var warnings []string
	p := selectUsageProvider(clients, &scanFlags{window: "14d"}, &warnings)
	if p.Name() != "metrics-server" {
		t.Errorf("provider = %q, want metrics-server", p.Name())
	}
}
