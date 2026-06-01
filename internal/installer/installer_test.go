package installer

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestEmbeddedManifestsNonEmpty(t *testing.T) {
	if len(CRDManifest()) == 0 {
		t.Error("CRDManifest is empty")
	}
	if len(OperatorManifest()) == 0 {
		t.Error("OperatorManifest is empty")
	}
	if !strings.Contains(string(CRDManifest()), "usageprofiles.kubetidy.io") {
		t.Error("CRD manifest missing the CRD name")
	}
	if !strings.Contains(string(OperatorManifest()), "kubetidy-operator") {
		t.Error("operator manifest missing the operator deployment")
	}
}

func TestDecodeObjectsCRD(t *testing.T) {
	objs, err := decodeObjects(CRDManifest())
	if err != nil {
		t.Fatalf("decodeObjects(CRD) error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("CRD manifest decoded to %d objects, want 1", len(objs))
	}
	if objs[0].GetKind() != "CustomResourceDefinition" {
		t.Errorf("kind = %q, want CustomResourceDefinition", objs[0].GetKind())
	}
}

func TestDecodeObjectsMultiDoc(t *testing.T) {
	objs, err := decodeObjects(OperatorManifest())
	if err != nil {
		t.Fatalf("decodeObjects(operator) error: %v", err)
	}
	// Namespace, ServiceAccount, ClusterRole, ClusterRoleBinding, Deployment = 5 documents.
	if len(objs) != 5 {
		t.Fatalf("operator manifest decoded to %d objects, want 5", len(objs))
	}
	kinds := map[string]bool{}
	for _, o := range objs {
		kinds[o.GetKind()] = true
	}
	for _, want := range []string{"Namespace", "ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Deployment"} {
		if !kinds[want] {
			t.Errorf("operator manifest missing a %s", want)
		}
	}
}

func TestDecodeObjectsSkipsEmptyDocuments(t *testing.T) {
	manifest := []byte("---\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n---\n")
	objs, err := decodeObjects(manifest)
	if err != nil {
		t.Fatalf("decodeObjects error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("got %d objects, want 1 (empty docs skipped)", len(objs))
	}
	if objs[0].GetName() != "x" {
		t.Errorf("name = %q, want x", objs[0].GetName())
	}
}

func TestDecodeObjectsInvalidYAML(t *testing.T) {
	if _, err := decodeObjects([]byte("\tnot: [valid")); err == nil {
		t.Error("expected an error decoding malformed YAML")
	}
}

func TestCRDEstablished(t *testing.T) {
	established := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "NamesAccepted", "status": "True"},
				map[string]any{"type": "Established", "status": "True"},
			},
		},
	}}
	if !crdEstablished(established) {
		t.Error("expected crdEstablished true when Established=True present")
	}

	notYet := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Established", "status": "False"},
			},
		},
	}}
	if crdEstablished(notYet) {
		t.Error("expected crdEstablished false when Established=False")
	}

	if crdEstablished(&unstructured.Unstructured{Object: map[string]any{}}) {
		t.Error("expected crdEstablished false when no status/conditions")
	}
}

func TestNewRESTMapperNilDiscovery(t *testing.T) {
	if _, err := newRESTMapper(nil); err == nil {
		t.Error("expected error for nil discovery client")
	}
}

func TestBoolPtr(t *testing.T) {
	if p := boolPtr(true); p == nil || !*p {
		t.Error("boolPtr(true) should point to true")
	}
}
