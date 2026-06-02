package installer

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
