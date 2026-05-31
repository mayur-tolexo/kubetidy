package installer

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

// emptyDiscovery is a discovery client that advertises no API resources, so any RESTMapping
// lookup fails — used to deterministically exercise applyObject's "no REST mapping" branch.
func emptyDiscovery() *fakediscovery.FakeDiscovery {
	return &fakediscovery.FakeDiscovery{Fake: &ktesting.Fake{}}
}

func TestApplyManifestNoMappingErrors(t *testing.T) {
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
	)
	mapper, err := newRESTMapper(emptyDiscovery())
	if err != nil {
		t.Fatalf("newRESTMapper error: %v", err)
	}
	// The CRD manifest's kind cannot be resolved by an empty discovery, so applyManifest must
	// surface a REST-mapping error (exercising decodeObjects + applyObject's error path).
	if err := applyManifest(context.Background(), dyn, mapper, CRDManifest()); err == nil ||
		!strings.Contains(err.Error(), "REST mapping") {
		t.Fatalf("err = %v, want a REST mapping error", err)
	}
}
