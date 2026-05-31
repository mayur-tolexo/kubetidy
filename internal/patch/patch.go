// Package patch turns an action-ready Recommendation into the exact, reversible Kubernetes
// change that would apply it: a strategic-merge patch and a copy-pasteable `kubectl patch`
// command. The MVP only prints these (it never mutates the cluster); this is the foundation
// for the Phase-2 GitOps-PR flow. The same Recommendation type feeds both, so action is an
// additive consumer, not a rewrite.
package patch

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/kubetidy/kubetidy/internal/model"
)

// The nested shape below mirrors a workload's pod template
// (spec.template.spec.containers[].resources), so it is a valid strategic-merge patch for
// Deployments, StatefulSets and DaemonSets alike.

type resourcesDoc struct {
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

type containerDoc struct {
	Name      string       `json:"name"`
	Resources resourcesDoc `json:"resources"`
}

type podSpecDoc struct {
	Containers []containerDoc `json:"containers"`
}

type templateDoc struct {
	Spec podSpecDoc `json:"spec"`
}

type specDoc struct {
	Template templateDoc `json:"template"`
}

type patchDoc struct {
	Spec specDoc `json:"spec"`
}

// StrategicMergePatch builds the strategic-merge patch JSON that sets the recommended
// requests (and limits, when the proposal includes them) on the recommendation's container.
// Unset (zero) quantities are omitted so the patch only touches what it must.
func StrategicMergePatch(rec model.Recommendation) ([]byte, error) {
	if rec.ContainerName == "" {
		return nil, fmt.Errorf("patch: recommendation has no container name")
	}
	doc := patchDoc{Spec: specDoc{Template: templateDoc{Spec: podSpecDoc{
		Containers: []containerDoc{{
			Name: rec.ContainerName,
			Resources: resourcesDoc{
				Requests: quantityMap(rec.Proposed.Requests),
				Limits:   quantityMap(rec.Proposed.Limits),
			},
		}},
	}}}}
	return json.Marshal(doc)
}

// KubectlCommand renders a copy-pasteable, reviewable `kubectl patch` command that applies
// the recommendation. It is reversible by construction: a human reviews it, runs it, or
// discards it — kubetidy never executes it.
func KubectlCommand(rec model.Recommendation) (string, error) {
	p, err := StrategicMergePatch(rec)
	if err != nil {
		return "", err
	}
	nsFlag := ""
	if rec.Workload.Namespace != "" {
		nsFlag = fmt.Sprintf(" -n %s", rec.Workload.Namespace)
	}
	return fmt.Sprintf("kubectl patch %s %s%s --type=strategic -p '%s'",
		kindArg(rec.Workload.Kind), rec.Workload.Name, nsFlag, string(p)), nil
}

// quantityMap renders CPU/memory amounts as canonical Kubernetes quantity strings
// (e.g. "320m", "1126Mi"). Returns nil when nothing is set, so the enclosing requests/limits
// key is omitted entirely.
func quantityMap(a model.ResourceAmounts) map[string]string {
	m := map[string]string{}
	if a.CPUMillicores > 0 {
		m["cpu"] = resource.NewMilliQuantity(a.CPUMillicores, resource.DecimalSI).String()
	}
	if a.MemoryBytes > 0 {
		m["memory"] = resource.NewQuantity(a.MemoryBytes, resource.BinarySI).String()
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// kindArg maps a workload kind to its kubectl resource argument.
func kindArg(k model.WorkloadKind) string {
	switch k {
	case model.KindDeployment:
		return "deployment"
	case model.KindStatefulSet:
		return "statefulset"
	case model.KindDaemonSet:
		return "daemonset"
	default:
		return string(k)
	}
}
