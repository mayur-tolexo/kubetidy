package installer

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
)

// crdObject builds an unstructured CustomResourceDefinition with the given Established status.
func crdObject(name string, established bool) *unstructured.Unstructured {
	status := "False"
	if established {
		status = "True"
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": name},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Established", "status": status},
			},
		},
	}}
	return obj
}

func fakeDynForCRD(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		crdGVR: "CustomResourceDefinitionList",
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func TestWaitCRDEstablishedSucceeds(t *testing.T) {
	dyn := fakeDynForCRD(crdObject("usageprofiles.kubetidy.io", true))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := waitCRDEstablished(ctx, dyn, "usageprofiles.kubetidy.io"); err != nil {
		t.Fatalf("waitCRDEstablished error: %v", err)
	}
}

func TestWaitCRDEstablishedContextCancelled(t *testing.T) {
	// CRD exists but is never established → the wait should give up when the context is done.
	dyn := fakeDynForCRD(crdObject("usageprofiles.kubetidy.io", false))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	if err := waitCRDEstablished(ctx, dyn, "usageprofiles.kubetidy.io"); err == nil {
		t.Error("expected an error when the context is cancelled before establishment")
	}
}
