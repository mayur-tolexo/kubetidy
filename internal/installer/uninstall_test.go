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
	ktesting "k8s.io/client-go/testing"
)

// fakeDiscovery returns a discovery client advertising the API resources Uninstall maps: the
// CRD type, core (Namespace, ServiceAccount), apps (Deployment), and rbac (ClusterRole,
// ClusterRoleBinding).
func fakeDiscovery() *fakediscovery.FakeDiscovery {
	return &fakediscovery.FakeDiscovery{
		Fake: &ktesting.Fake{
			Resources: []*metav1.APIResourceList{
				{
					GroupVersion: "apiextensions.k8s.io/v1",
					APIResources: []metav1.APIResource{
						{Name: "customresourcedefinitions", Kind: "CustomResourceDefinition", Namespaced: false},
					},
				},
				{
					GroupVersion: "v1",
					APIResources: []metav1.APIResource{
						{Name: "namespaces", Kind: "Namespace", Namespaced: false},
						{Name: "serviceaccounts", Kind: "ServiceAccount", Namespaced: true},
					},
				},
				{
					GroupVersion: "apps/v1",
					APIResources: []metav1.APIResource{
						{Name: "deployments", Kind: "Deployment", Namespaced: true},
					},
				},
				{
					GroupVersion: "rbac.authorization.k8s.io/v1",
					APIResources: []metav1.APIResource{
						{Name: "clusterroles", Kind: "ClusterRole", Namespaced: false},
						{Name: "clusterrolebindings", Kind: "ClusterRoleBinding", Namespaced: false},
					},
				},
			},
		},
	}
}

// uninstallScheme registers list kinds for every resource Uninstall deletes, so the fake
// dynamic client can serve them.
func uninstallFakeDynamic(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}: "CustomResourceDefinitionList",
		{Group: "apps", Version: "v1", Resource: "deployments"}:                               "DeploymentList",
		{Group: "", Version: "v1", Resource: "namespaces"}:                                    "NamespaceList",
		{Group: "", Version: "v1", Resource: "serviceaccounts"}:                               "ServiceAccountList",
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}:         "ClusterRoleList",
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}:  "ClusterRoleBindingList",
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func TestDeleteObjectToleratesNotFound(t *testing.T) {
	dyn := uninstallFakeDynamic()
	disco := fakeDiscovery() // from installer_test.go: advertises the operator + CRD resources
	mapper, err := newRESTMapper(disco)
	if err != nil {
		t.Fatalf("newRESTMapper: %v", err)
	}

	// Deleting from an empty cluster must succeed (idempotent: not-found is OK).
	if err := deleteManifest(context.Background(), dyn, mapper, OperatorManifest(), UninstallOptions{}); err != nil {
		t.Fatalf("deleteManifest on empty cluster should be a no-op, got: %v", err)
	}
}

func TestDeleteObjectRemovesExisting(t *testing.T) {
	// Seed a CRD object, then confirm deleteObject removes it.
	crd := crdObject("usageprofiles.kubetidy.io", true) // from wait_test.go
	dyn := uninstallFakeDynamic(crd)
	disco := fakeDiscovery()
	mapper, err := newRESTMapper(disco)
	if err != nil {
		t.Fatalf("newRESTMapper: %v", err)
	}

	gvr := crdGVR
	if _, err := dyn.Resource(gvr).Get(context.Background(), "usageprofiles.kubetidy.io", metav1.GetOptions{}); err != nil {
		t.Fatalf("seed CRD not retrievable: %v", err)
	}
	if err := deleteObject(context.Background(), dyn, mapper, crd, UninstallOptions{}); err != nil {
		t.Fatalf("deleteObject: %v", err)
	}
	if _, err := dyn.Resource(gvr).Get(context.Background(), "usageprofiles.kubetidy.io", metav1.GetOptions{}); err == nil {
		t.Error("CRD should have been deleted")
	}
}

func TestUninstallKeepCRDs(t *testing.T) {
	dyn := uninstallFakeDynamic()
	var logs []string
	// keepCRDs=true deletes only the operator; an empty cluster makes every delete a no-op,
	// so this exercises the operator-only path without error.
	if err := Uninstall(context.Background(), dyn, fakeDiscovery(), UninstallOptions{
		KeepCRDs: true,
		Log:      func(m string) { logs = append(logs, m) },
	}); err != nil {
		t.Fatalf("Uninstall(keepCRDs) error: %v", err)
	}
	if len(logs) == 0 {
		t.Error("expected progress logs")
	}
}

func TestUninstallFull(t *testing.T) {
	dyn := uninstallFakeDynamic()
	if err := Uninstall(context.Background(), dyn, fakeDiscovery(), UninstallOptions{}); err != nil {
		t.Fatalf("Uninstall(full) error: %v", err)
	}
}

func TestUninstallDryRun(t *testing.T) {
	// Seed a CRD; dry-run must report it as present but NOT delete it.
	crd := crdObject("usageprofiles.kubetidy.io", true)
	dyn := uninstallFakeDynamic(crd)
	var logs []string
	if err := Uninstall(context.Background(), dyn, fakeDiscovery(), UninstallOptions{
		DryRun: true,
		Log:    func(m string) { logs = append(logs, m) },
	}); err != nil {
		t.Fatalf("Uninstall(dry-run) error: %v", err)
	}
	// The CRD must still exist after a dry run.
	if _, err := dyn.Resource(crdGVR).Get(context.Background(), "usageprofiles.kubetidy.io", metav1.GetOptions{}); err != nil {
		t.Errorf("dry run should not delete the CRD, but it is gone: %v", err)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "would delete") {
		t.Errorf("dry-run logs should say 'would delete', got: %s", joined)
	}
}

func TestUninstallNilDiscovery(t *testing.T) {
	dyn := uninstallFakeDynamic()
	if err := Uninstall(context.Background(), dyn, nil, UninstallOptions{}); err == nil {
		t.Error("expected an error with a nil discovery client")
	}
}
