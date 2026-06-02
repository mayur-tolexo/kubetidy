// Package cleanup detects removable "junk" in a cluster — the literal tidy: orphaned Services,
// unused PersistentVolumeClaims, idle namespaces, and zombie (scaled-to-zero) workloads. It is
// PURE: callers gather the live objects (via client-go) and pass them in; this package only
// reasons over them, so it is fully unit-testable with fakes. It never mutates anything.
package cleanup

import (
	"fmt"
	"math"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// Category groups findings by the kind of cleanup opportunity.
type Category string

// The cleanup categories the sweep reports.
const (
	OrphanedService Category = "orphaned service"
	UnusedPVC       Category = "unused pvc"
	IdleNamespace   Category = "idle namespace"
	ZombieWorkload  Category = "zombie workload"
)

// Finding is one removable resource the sweep surfaces. It is read-only advice; nothing is
// deleted. MonthlyCost is a best-effort estimate (PVC storage) — 0 when not applicable.
type Finding struct {
	Category    Category
	Kind        string // Service, PersistentVolumeClaim, Namespace, Deployment, StatefulSet
	Namespace   string // empty for cluster-scoped objects (Namespace)
	Name        string
	Reason      string  // human-readable justification
	Detail      string  // optional extra (e.g. "50Gi", "scaled to 0")
	MonthlyCost float64 // estimated $/month reclaimable (PVC storage); 0 otherwise
}

// Inputs are the live cluster objects the detectors reason over. The caller lists them.
type Inputs struct {
	Services     []corev1.Service
	Pods         []corev1.Pod
	PVCs         []corev1.PersistentVolumeClaim
	Namespaces   []corev1.Namespace
	Deployments  []appsv1.Deployment
	StatefulSets []appsv1.StatefulSet
	DaemonSets   []appsv1.DaemonSet

	// StoragePerGiBMonth prices unused PVCs ($/GiB-month). 0 disables PVC cost estimates.
	StoragePerGiBMonth float64
}

// systemNamespaces are never flagged as idle (they legitimately hold cluster infra, sometimes
// with no user workloads of their own).
var systemNamespaces = map[string]bool{
	"kube-system": true, "kube-public": true, "kube-node-lease": true,
	"kubetidy-system": true, "default": true,
}

// Detect runs all detectors and returns the combined findings (orphaned services, unused PVCs,
// idle namespaces, zombie workloads), in that stable order.
func Detect(in Inputs) []Finding {
	var out []Finding
	out = append(out, orphanedServices(in)...)
	out = append(out, unusedPVCs(in)...)
	out = append(out, idleNamespaces(in)...)
	out = append(out, zombieWorkloads(in)...)
	return out
}

// orphanedServices finds Services whose selector matches no Pod — a dead routing target. It
// skips Services with no selector (headless / manually-managed endpoints) and ExternalName
// (which has no selector by design), since orphan-ness can't be inferred for those.
func orphanedServices(in Inputs) []Finding {
	var out []Finding
	for i := range in.Services {
		svc := &in.Services[i]
		if svc.Spec.Type == corev1.ServiceTypeExternalName || len(svc.Spec.Selector) == 0 {
			continue
		}
		sel := labels.SelectorFromSet(svc.Spec.Selector)
		matched := false
		for j := range in.Pods {
			p := &in.Pods[j]
			if p.Namespace == svc.Namespace && sel.Matches(labels.Set(p.Labels)) {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, Finding{
				Category:  OrphanedService,
				Kind:      "Service",
				Namespace: svc.Namespace,
				Name:      svc.Name,
				Reason:    "no pods match its selector",
				Detail:    selectorString(svc.Spec.Selector),
			})
		}
	}
	return out
}

// unusedPVCs finds PersistentVolumeClaims not mounted by any Pod — storage you pay for but
// nothing uses. Cost is estimated from the requested size and StoragePerGiBMonth.
func unusedPVCs(in Inputs) []Finding {
	// Build the set of PVCs referenced by some pod, keyed by namespace/name.
	used := make(map[string]bool)
	for i := range in.Pods {
		p := &in.Pods[i]
		for _, v := range p.Spec.Volumes {
			if v.PersistentVolumeClaim != nil {
				used[p.Namespace+"/"+v.PersistentVolumeClaim.ClaimName] = true
			}
		}
	}

	var out []Finding
	for i := range in.PVCs {
		pvc := &in.PVCs[i]
		if used[pvc.Namespace+"/"+pvc.Name] {
			continue
		}
		gib := pvcGiB(pvc)
		out = append(out, Finding{
			Category:    UnusedPVC,
			Kind:        "PersistentVolumeClaim",
			Namespace:   pvc.Namespace,
			Name:        pvc.Name,
			Reason:      "not mounted by any pod",
			Detail:      formatGiB(gib),
			MonthlyCost: gib * in.StoragePerGiBMonth,
		})
	}
	return out
}

// idleNamespaces finds non-system namespaces with no running workloads: no Deployments,
// StatefulSets, or DaemonSets with desired replicas, and no Pods.
func idleNamespaces(in Inputs) []Finding {
	// Count "live" workload signal per namespace.
	live := make(map[string]bool)
	for i := range in.Deployments {
		if replicas(in.Deployments[i].Spec.Replicas) > 0 {
			live[in.Deployments[i].Namespace] = true
		}
	}
	for i := range in.StatefulSets {
		if replicas(in.StatefulSets[i].Spec.Replicas) > 0 {
			live[in.StatefulSets[i].Namespace] = true
		}
	}
	for i := range in.DaemonSets {
		live[in.DaemonSets[i].Namespace] = true // a DaemonSet always wants a pod per node
	}
	for i := range in.Pods {
		live[in.Pods[i].Namespace] = true
	}

	var out []Finding
	for i := range in.Namespaces {
		ns := &in.Namespaces[i]
		if systemNamespaces[ns.Name] || ns.Status.Phase == corev1.NamespaceTerminating {
			continue
		}
		if !live[ns.Name] {
			out = append(out, Finding{
				Category: IdleNamespace,
				Kind:     "Namespace",
				Name:     ns.Name,
				Reason:   "no running workloads",
			})
		}
	}
	return out
}

// zombieWorkloads finds Deployments/StatefulSets explicitly scaled to zero — defined but doing
// nothing, often a forgotten scale-down.
func zombieWorkloads(in Inputs) []Finding {
	var out []Finding
	for i := range in.Deployments {
		d := &in.Deployments[i]
		if replicas(d.Spec.Replicas) == 0 {
			out = append(out, Finding{
				Category: ZombieWorkload, Kind: "Deployment", Namespace: d.Namespace, Name: d.Name,
				Reason: "scaled to 0 replicas",
			})
		}
	}
	for i := range in.StatefulSets {
		s := &in.StatefulSets[i]
		if replicas(s.Spec.Replicas) == 0 {
			out = append(out, Finding{
				Category: ZombieWorkload, Kind: "StatefulSet", Namespace: s.Namespace, Name: s.Name,
				Reason: "scaled to 0 replicas",
			})
		}
	}
	return out
}

// TotalMonthlyCost sums the estimated reclaimable $/month across findings.
func TotalMonthlyCost(findings []Finding) float64 {
	var sum float64
	for _, f := range findings {
		sum += f.MonthlyCost
	}
	return sum
}

// replicas returns the desired replica count (nil defaults to 1, matching Kubernetes).
func replicas(r *int32) int32 {
	if r == nil {
		return 1
	}
	return *r
}

// pvcGiB returns the PVC's requested size in GiB (0 when unset).
func pvcGiB(pvc *corev1.PersistentVolumeClaim) float64 {
	q, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if !ok {
		return 0
	}
	return float64(q.Value()) / (1 << 30)
}

func formatGiB(gib float64) string {
	switch {
	case gib <= 0:
		return "size unknown"
	case gib == math.Trunc(gib):
		return fmt.Sprintf("%dGi", int64(gib))
	default:
		return fmt.Sprintf("%.1fGi", gib)
	}
}

func selectorString(sel map[string]string) string {
	return labels.SelectorFromSet(sel).String()
}
