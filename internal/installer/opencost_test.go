package installer

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynfake "k8s.io/client-go/dynamic/fake"
)

func TestOpenCostManifestSubstitutesPrometheusURL(t *testing.T) {
	const custom = "http://prom.observability.svc:9090"
	m := string(OpenCostManifest(custom))
	if strings.Contains(m, opencostPrometheusPlaceholder) {
		t.Errorf("placeholder %q not substituted", opencostPrometheusPlaceholder)
	}
	if !strings.Contains(m, custom) {
		t.Errorf("manifest missing the custom Prometheus URL %q", custom)
	}
}

func TestOpenCostManifestDefaultsPrometheusURL(t *testing.T) {
	m := string(OpenCostManifest(""))
	if !strings.Contains(m, defaultPrometheusURL) {
		t.Errorf("empty URL should default to %q", defaultPrometheusURL)
	}
	if strings.Contains(m, opencostPrometheusPlaceholder) {
		t.Error("placeholder should be substituted even when defaulting")
	}
}

func TestDecodeOpenCostObjects(t *testing.T) {
	objs, err := decodeObjects(OpenCostManifest(""))
	if err != nil {
		t.Fatalf("decodeObjects(opencost) error: %v", err)
	}
	// Namespace, ServiceAccount, ClusterRole, ClusterRoleBinding, Deployment, Service = 6.
	if len(objs) != 6 {
		t.Fatalf("opencost manifest decoded to %d objects, want 6", len(objs))
	}
	kinds := map[string]bool{}
	for _, o := range objs {
		kinds[o.GetKind()] = true
	}
	for _, want := range []string{"Namespace", "ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Deployment", "Service"} {
		if !kinds[want] {
			t.Errorf("opencost manifest missing a %s", want)
		}
	}
}

// fakeDiscoveryWithServices extends the operator discovery set with the core Service resource,
// which the OpenCost manifest needs to map.
func fakeDiscoveryWithServices() *fakediscovery.FakeDiscovery {
	d := fakeDiscovery()
	for _, list := range d.Resources {
		if list.GroupVersion == "v1" {
			list.APIResources = append(list.APIResources,
				metav1.APIResource{Name: "services", Kind: "Service", Namespaced: true})
		}
	}
	return d
}

func openCostFakeDynamic(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}: "CustomResourceDefinitionList",
		{Group: "apps", Version: "v1", Resource: "deployments"}:                               "DeploymentList",
		{Group: "", Version: "v1", Resource: "namespaces"}:                                    "NamespaceList",
		{Group: "", Version: "v1", Resource: "serviceaccounts"}:                               "ServiceAccountList",
		{Group: "", Version: "v1", Resource: "services"}:                                      "ServiceList",
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}:         "ClusterRoleList",
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}:  "ClusterRoleBindingList",
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func TestDecodePrometheusObjects(t *testing.T) {
	objs, err := decodeObjects(PrometheusManifest())
	if err != nil {
		t.Fatalf("decodeObjects(prometheus) error: %v", err)
	}
	// Prometheus: Namespace, ServiceAccount, ClusterRole, ClusterRoleBinding, ConfigMap,
	// Deployment, Service (7). Bundled kube-state-metrics: ServiceAccount, ClusterRole,
	// ClusterRoleBinding, Deployment, Service (5). Total 12.
	if len(objs) != 12 {
		t.Fatalf("prometheus manifest decoded to %d objects, want 12", len(objs))
	}
	kinds := map[string]bool{}
	for _, o := range objs {
		kinds[o.GetKind()] = true
	}
	for _, want := range []string{"Namespace", "ServiceAccount", "ClusterRole", "ClusterRoleBinding", "ConfigMap", "Deployment", "Service"} {
		if !kinds[want] {
			t.Errorf("prometheus manifest missing a %s", want)
		}
	}
}

func TestPrometheusManifestServesDefaultEndpoint(t *testing.T) {
	// The bundled Prometheus must live where OpenCost's default endpoint and kubetidy's
	// auto-detect both look: service prometheus-server in namespace monitoring on port 80.
	m := string(PrometheusManifest())
	if !strings.Contains(m, "name: prometheus-server") {
		t.Error("bundled Prometheus service must be named prometheus-server")
	}
	if !strings.Contains(m, "namespace: monitoring") {
		t.Error("bundled Prometheus must be in the monitoring namespace")
	}
}

func TestPrometheusManifestClosesOpenCostScrapeLoop(t *testing.T) {
	// OpenCost computes cost only if Prometheus scrapes OpenCost's own /metrics and
	// kube-state-metrics; assert both the scrape jobs and the bundled KSM are present.
	m := string(PrometheusManifest())
	for _, want := range []string{
		"job_name: opencost",
		"opencost.opencost.svc:9003",
		"job_name: kube-state-metrics",
		"kubetidy-kube-state-metrics.monitoring.svc:8080",
		"image: registry.k8s.io/kube-state-metrics/kube-state-metrics",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("bundled Prometheus manifest missing %q", want)
		}
	}
}

func openCostFakeDynamicWithConfigMaps(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}: "CustomResourceDefinitionList",
		{Group: "apps", Version: "v1", Resource: "deployments"}:                               "DeploymentList",
		{Group: "", Version: "v1", Resource: "namespaces"}:                                    "NamespaceList",
		{Group: "", Version: "v1", Resource: "serviceaccounts"}:                               "ServiceAccountList",
		{Group: "", Version: "v1", Resource: "services"}:                                      "ServiceList",
		{Group: "", Version: "v1", Resource: "configmaps"}:                                    "ConfigMapList",
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}:         "ClusterRoleList",
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}:  "ClusterRoleBindingList",
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func fakeDiscoveryWithConfigMaps() *fakediscovery.FakeDiscovery {
	d := fakeDiscoveryWithServices()
	for _, list := range d.Resources {
		if list.GroupVersion == "v1" {
			list.APIResources = append(list.APIResources,
				metav1.APIResource{Name: "configmaps", Kind: "ConfigMap", Namespaced: true})
		}
	}
	return d
}

func TestUninstallIncludePrometheusKeepsNamespace(t *testing.T) {
	dyn := openCostFakeDynamicWithConfigMaps()
	var logs []string
	if err := Uninstall(context.Background(), dyn, fakeDiscoveryWithConfigMaps(), UninstallOptions{
		IncludePrometheus: true,
		Log:               func(m string) { logs = append(logs, m) },
	}); err != nil {
		t.Fatalf("Uninstall(IncludePrometheus) error: %v", err)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "Prometheus") {
		t.Error("expected Prometheus removal to be logged")
	}
	if !strings.Contains(joined, "keeping the namespace") {
		t.Error("expected the log to note the namespace is kept")
	}
}

func TestDeleteManifestSkipNamespaces(t *testing.T) {
	// Seed the monitoring Namespace; deleting the Prometheus manifest with the skip variant must
	// leave it intact.
	ns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace",
		"metadata": map[string]any{"name": "monitoring"},
	}}
	dyn := openCostFakeDynamicWithConfigMaps(ns)
	mapper, err := newRESTMapper(fakeDiscoveryWithConfigMaps())
	if err != nil {
		t.Fatalf("newRESTMapper: %v", err)
	}
	if err := deleteManifestSkipNamespaces(context.Background(), dyn, mapper, PrometheusManifest(), UninstallOptions{}); err != nil {
		t.Fatalf("deleteManifestSkipNamespaces: %v", err)
	}
	nsGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	if _, err := dyn.Resource(nsGVR).Get(context.Background(), "monitoring", metav1.GetOptions{}); err != nil {
		t.Errorf("monitoring namespace should have been preserved: %v", err)
	}
}

func TestUninstallIncludeOpenCost(t *testing.T) {
	dyn := openCostFakeDynamic()
	var logs []string
	if err := Uninstall(context.Background(), dyn, fakeDiscoveryWithServices(), UninstallOptions{
		IncludeOpenCost: true,
		Log:             func(m string) { logs = append(logs, m) },
	}); err != nil {
		t.Fatalf("Uninstall(IncludeOpenCost) error: %v", err)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "OpenCost") {
		t.Error("expected OpenCost removal to be logged")
	}
}

func TestUninstallSkipsOpenCostByDefault(t *testing.T) {
	dyn := openCostFakeDynamic()
	var logs []string
	if err := Uninstall(context.Background(), dyn, fakeDiscoveryWithServices(), UninstallOptions{
		Log: func(m string) { logs = append(logs, m) },
	}); err != nil {
		t.Fatalf("Uninstall error: %v", err)
	}
	if strings.Contains(strings.Join(logs, "\n"), "OpenCost") {
		t.Error("OpenCost should not be touched without IncludeOpenCost")
	}
}
