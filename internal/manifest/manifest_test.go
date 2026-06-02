package manifest

import (
	"testing"

	"github.com/kubetidy/kubetidy/internal/model"
)

const sample = `
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  replicas: 3
  template:
    spec:
      containers:
        - name: app
          resources: {requests: {cpu: "500m", memory: "1Gi"}}
        - name: sidecar
          resources: {requests: {cpu: "100m", memory: "64Mi"}}
---
apiVersion: v1
kind: Service
metadata: {name: api, namespace: shop}
spec: {selector: {app: api}}
---
apiVersion: apps/v1
kind: DaemonSet
metadata: {name: agent, namespace: kube-system}
spec:
  template:
    spec:
      containers:
        - name: agent
          resources: {requests: {cpu: "50m", memory: "32Mi"}}
`

func TestParseWorkloads(t *testing.T) {
	ws, err := ParseWorkloadsBytes([]byte(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ws) != 2 {
		t.Fatalf("got %d workloads, want 2 (Service skipped)", len(ws))
	}
	dep := ws[0]
	if dep.Kind != "Deployment" || dep.Name != "api" || dep.Namespace != "shop" || dep.Replicas != 3 {
		t.Errorf("deployment parsed wrong: %+v", dep)
	}
	if len(dep.Containers) != 2 || dep.Containers[0].Requests.CPUMillicores != 500 || dep.Containers[0].Requests.MemoryBytes != 1<<30 {
		t.Errorf("container requests parsed wrong: %+v", dep.Containers)
	}
	ds := ws[1]
	if ds.Kind != "DaemonSet" || ds.Replicas != 1 { // DaemonSet defaults to 1
		t.Errorf("daemonset parsed wrong: %+v", ds)
	}
}

func TestParseWorkloads_NamespaceDefault(t *testing.T) {
	ws, _ := ParseWorkloadsBytes([]byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: x}\nspec: {template: {spec: {containers: [{name: c, resources: {requests: {cpu: \"1\"}}}]}}}\n"))
	if len(ws) != 1 || ws[0].Namespace != "default" {
		t.Errorf("want namespace default, got %+v", ws)
	}
}

func TestParseWorkloads_BadYAML(t *testing.T) {
	if _, err := ParseWorkloadsBytes([]byte("kind: Deployment\n\tbad: indent")); err == nil {
		t.Error("expected error on malformed YAML")
	}
}

func TestCompare(t *testing.T) {
	price := model.ResourcePrice{CPUCoreMonth: 24, MemGiBMonth: 3}
	base := CostWorkloads([]model.Workload{
		{Kind: "Deployment", Namespace: "shop", Name: "api", Replicas: 2, Containers: []model.Container{{Requests: model.ResourceAmounts{CPUMillicores: 500, MemoryBytes: 1 << 30}}}},
		{Kind: "Deployment", Namespace: "shop", Name: "gone", Replicas: 1, Containers: []model.Container{{Requests: model.ResourceAmounts{CPUMillicores: 1000}}}},
	}, price)
	head := CostWorkloads([]model.Workload{
		{Kind: "Deployment", Namespace: "shop", Name: "api", Replicas: 4, Containers: []model.Container{{Requests: model.ResourceAmounts{CPUMillicores: 1000, MemoryBytes: 1 << 30}}}},
		{Kind: "Deployment", Namespace: "shop", Name: "new", Replicas: 1, Containers: []model.Container{{Requests: model.ResourceAmounts{CPUMillicores: 500}}}},
	}, price)

	rep := Compare(base, head)
	if rep.NetDelta <= 0 {
		t.Errorf("expected a net increase, got %.2f", rep.NetDelta)
	}
	byRef := map[string]CostChange{}
	for _, c := range rep.Changes {
		byRef[c.Ref] = c
	}
	if byRef["Deployment/shop/api"].Status != Changed {
		t.Errorf("api should be 'changed': %+v", byRef["Deployment/shop/api"])
	}
	if byRef["Deployment/shop/new"].Status != Added {
		t.Errorf("new should be 'added'")
	}
	if byRef["Deployment/shop/gone"].Status != Removed {
		t.Errorf("gone should be 'removed'")
	}
}
