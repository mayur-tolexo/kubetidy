// Package manifest parses Kubernetes YAML/JSON manifests into model.Workloads (kind, name,
// namespace, replicas, and per-container resource requests) — without a cluster. It powers the
// CI cost-guardrail (`kubetidy cost`), which compares the cost of a PR's manifests before/after.
// It is PURE: bytes in, workloads out.
package manifest

import (
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/kubetidy/kubetidy/internal/model"
)

// workloadKinds maps the manifest kinds we cost to where their pod template + replicas live.
// DaemonSets have no replicas field (one pod per node); we treat them as a single replica for
// a stable per-pod cost figure.
var workloadKinds = map[string]bool{
	"Deployment": true, "StatefulSet": true, "DaemonSet": true, "ReplicaSet": true,
}

// ParseWorkloads decodes a (possibly multi-document) YAML/JSON stream and returns the workloads
// it can cost. Non-workload documents and empty docs are skipped. A malformed document is a
// hard error (so a typo in a manifest doesn't silently under-count cost).
func ParseWorkloads(r io.Reader) ([]model.Workload, error) {
	dec := utilyaml.NewYAMLOrJSONDecoder(r, 4096)
	var out []model.Workload
	for {
		obj := &unstructured.Unstructured{}
		err := dec.Decode(obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("manifest: decoding document: %w", err)
		}
		if len(obj.Object) == 0 {
			continue
		}
		if !workloadKinds[obj.GetKind()] {
			continue
		}
		w, ok, err := workloadFromUnstructured(obj)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, w)
		}
	}
	return out, nil
}

// ParseWorkloadsBytes is a convenience wrapper over ParseWorkloads.
func ParseWorkloadsBytes(b []byte) ([]model.Workload, error) {
	return ParseWorkloads(strings.NewReader(string(b)))
}

func workloadFromUnstructured(obj *unstructured.Unstructured) (model.Workload, bool, error) {
	kind := obj.GetKind()
	w := model.Workload{
		Kind:      model.WorkloadKind(kind),
		Name:      obj.GetName(),
		Namespace: namespaceOrDefault(obj.GetNamespace()),
		Replicas:  1,
	}

	if kind != "DaemonSet" {
		if r, found, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas"); found {
			w.Replicas = int32(r)
		}
	}

	containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		return model.Workload{}, false, nil // no pod template → nothing to cost
	}
	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		w.Containers = append(w.Containers, containerFromMap(cm))
	}
	if len(w.Containers) == 0 {
		return model.Workload{}, false, nil
	}
	return w, true, nil
}

func containerFromMap(cm map[string]any) model.Container {
	name, _, _ := unstructured.NestedString(cm, "name")
	c := model.Container{Name: name}
	if req, found, _ := unstructured.NestedStringMap(cm, "resources", "requests"); found {
		c.Requests = amountsFrom(req)
	}
	if lim, found, _ := unstructured.NestedStringMap(cm, "resources", "limits"); found {
		c.Limits = amountsFrom(lim)
	}
	return c
}

// amountsFrom converts a requests/limits map ({"cpu":"500m","memory":"1Gi"}) to normalized
// millicores + bytes. Unparseable quantities are treated as zero.
func amountsFrom(m map[string]string) model.ResourceAmounts {
	var a model.ResourceAmounts
	if v, ok := m[string(corev1.ResourceCPU)]; ok {
		if q, err := resource.ParseQuantity(v); err == nil {
			a.CPUMillicores = q.MilliValue()
		}
	}
	if v, ok := m[string(corev1.ResourceMemory)]; ok {
		if q, err := resource.ParseQuantity(v); err == nil {
			a.MemoryBytes = q.Value()
		}
	}
	return a
}

func namespaceOrDefault(ns string) string {
	if ns == "" {
		return "default"
	}
	return ns
}
