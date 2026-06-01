// Package installer applies kubetidy's in-cluster components (the UsageProfile CRD and the
// operator's Deployment + RBAC) from manifests embedded in the binary. It exists so that
// `kubectl tidy init` can set everything up with a single command — no separate `kubectl
// apply -f` of files the user has to locate.
//
// Manifests are embedded via go:embed and applied through the dynamic client using
// server-side apply, which is idempotent: running init repeatedly converges the cluster to
// the embedded manifests without create/update bookkeeping. The CRD is applied first and we
// wait for it to become Established before applying anything else, so resources that depend on
// it never race the API server.
package installer

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

//go:embed assets/usageprofiles.yaml
var usageProfileCRD []byte

//go:embed assets/clusterusagesummaries.yaml
var clusterUsageSummaryCRD []byte

//go:embed assets/recommendations.yaml
var recommendationCRD []byte

//go:embed assets/operator.yaml
var operatorManifest []byte

// crdManifest is the concatenation of all kubetidy CRDs, applied together by init. The
// UsageProfile CRD is first so we can wait on it specifically before deploying the operator.
var crdManifest = joinManifests(usageProfileCRD, clusterUsageSummaryCRD, recommendationCRD)

// joinManifests concatenates YAML documents with a separator so they decode as a multi-doc
// stream.
func joinManifests(docs ...[]byte) []byte {
	var out []byte
	for i, d := range docs {
		if i > 0 {
			out = append(out, []byte("\n---\n")...)
		}
		out = append(out, d...)
	}
	return out
}

// fieldManager identifies kubetidy as the owner of the fields it applies (server-side apply).
const fieldManager = "kubetidy"

// crdGVR is the fixed coordinate of the CustomResourceDefinition resource, used to poll for
// the UsageProfile CRD becoming Established.
var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

// Options tunes an install.
type Options struct {
	// IncludeOperator controls whether the operator Deployment + RBAC are applied in addition
	// to the CRD. When false, only the CRD is installed (useful for GitOps setups that manage
	// the Deployment separately).
	IncludeOperator bool
	// Image, when non-empty, overrides the operator container image in the embedded manifest
	// (defaultOperatorImage). Use it to pin a version tag or point at a private mirror.
	Image string
	// Log receives one-line progress messages. nil discards them.
	Log func(string)
}

// defaultOperatorImage is the image hard-coded in the embedded operator manifest. It is the
// published Docker Hub image; callers may override it with Options.Image (e.g. a private
// registry mirror or a specific version tag).
const defaultOperatorImage = "docker.io/mayurdas1991/kubetidy-operator:latest"

func (o Options) log(msg string) {
	if o.Log != nil {
		o.Log(msg)
	}
}

// Install applies the embedded manifests to the cluster: the CRD first (waiting for it to be
// Established), then — when opts.IncludeOperator — the operator Deployment and RBAC.
func Install(ctx context.Context, dyn dynamic.Interface, disco discovery.DiscoveryInterface, opts Options) error {
	mapper, err := newRESTMapper(disco)
	if err != nil {
		return fmt.Errorf("installer: building REST mapper: %w", err)
	}

	opts.log("applying kubetidy CRDs (UsageProfile, ClusterUsageSummary, Recommendation)")
	if err := applyManifest(ctx, dyn, mapper, crdManifest); err != nil {
		return err
	}
	opts.log("waiting for CRDs to become established")
	for _, name := range []string{
		"usageprofiles.kubetidy.io",
		"clusterusagesummaries.kubetidy.io",
		"recommendations.kubetidy.io",
	} {
		if err := waitCRDEstablished(ctx, dyn, name); err != nil {
			return err
		}
	}

	if !opts.IncludeOperator {
		opts.log("CRD installed (operator skipped)")
		return nil
	}

	opts.log("applying operator (namespace, RBAC, deployment)")
	manifest := operatorManifest
	if opts.Image != "" {
		manifest = []byte(strings.ReplaceAll(string(operatorManifest), defaultOperatorImage, opts.Image))
	}
	if err := applyManifest(ctx, dyn, mapper, manifest); err != nil {
		return err
	}
	opts.log("install complete")
	return nil
}

// CRDManifest returns the embedded kubetidy CRD YAML (all three CRDs), for callers that want
// to print it (e.g. `init --print`) instead of applying it.
func CRDManifest() []byte { return crdManifest }

// OperatorManifest returns the embedded operator (namespace, RBAC, Deployment) YAML, for
// callers that want to print it instead of applying it.
func OperatorManifest() []byte { return operatorManifest }

// applyManifest decodes a (possibly multi-document) YAML manifest and server-side-applies each
// object, resolving each object's GroupVersionKind to a resource via the REST mapper.
func applyManifest(ctx context.Context, dyn dynamic.Interface, mapper *restmapper.DeferredDiscoveryRESTMapper, manifest []byte) error {
	objs, err := decodeObjects(manifest)
	if err != nil {
		return err
	}
	for _, obj := range objs {
		if err := applyObject(ctx, dyn, mapper, obj); err != nil {
			return err
		}
	}
	return nil
}

// applyObject server-side-applies a single unstructured object.
func applyObject(ctx context.Context, dyn dynamic.Interface, mapper *restmapper.DeferredDiscoveryRESTMapper, obj *unstructured.Unstructured) error {
	gvk := obj.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("installer: no REST mapping for %s: %w", gvk, err)
	}

	var ri dynamic.ResourceInterface
	if mapping.Scope.Name() == "namespace" {
		ns := obj.GetNamespace()
		if ns == "" {
			ns = "default"
		}
		ri = dyn.Resource(mapping.Resource).Namespace(ns)
	} else {
		ri = dyn.Resource(mapping.Resource)
	}

	data, err := obj.MarshalJSON()
	if err != nil {
		return fmt.Errorf("installer: marshalling %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}
	_, err = ri.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
		FieldManager: fieldManager,
		Force:        boolPtr(true),
	})
	if err != nil {
		return fmt.Errorf("installer: applying %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}
	return nil
}

// decodeObjects splits a multi-document YAML stream into unstructured objects, skipping empty
// documents.
func decodeObjects(manifest []byte) ([]*unstructured.Unstructured, error) {
	dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(string(manifest)), 4096)
	var out []*unstructured.Unstructured
	for {
		obj := &unstructured.Unstructured{}
		err := dec.Decode(obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("installer: decoding manifest: %w", err)
		}
		if len(obj.Object) == 0 {
			continue // empty document (e.g. trailing ---)
		}
		out = append(out, obj)
	}
	return out, nil
}

// waitCRDEstablished polls the named CRD until its Established condition is True, or ctx /
// a short deadline expires. This prevents applying a custom resource before the API server is
// serving its type.
func waitCRDEstablished(ctx context.Context, dyn dynamic.Interface, name string) error {
	deadline := time.Now().Add(60 * time.Second)
	for {
		obj, err := dyn.Resource(crdGVR).Get(ctx, name, metav1.GetOptions{})
		if err == nil && crdEstablished(obj) {
			return nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("installer: checking CRD %s: %w", name, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("installer: timed out waiting for CRD %s to become established", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// crdEstablished reports whether a CRD object has the Established=True condition.
func crdEstablished(obj *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cond["type"] == "Established" && cond["status"] == "True" {
			return true
		}
	}
	return false
}

// newRESTMapper builds a discovery-backed REST mapper that resolves a GroupVersionKind to the
// resource the dynamic client needs.
func newRESTMapper(disco discovery.DiscoveryInterface) (*restmapper.DeferredDiscoveryRESTMapper, error) {
	if disco == nil {
		return nil, fmt.Errorf("nil discovery client")
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disco)), nil
}

func boolPtr(b bool) *bool { return &b }
