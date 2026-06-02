package manifest

import (
	"sort"

	"github.com/kubetidy/kubetidy/internal/costmodel"
	"github.com/kubetidy/kubetidy/internal/model"
	"github.com/kubetidy/kubetidy/internal/pricing"
)

// WorkloadCost is the estimated monthly request cost of one workload (all containers × replicas).
type WorkloadCost struct {
	Kind      string
	Namespace string
	Name      string
	Replicas  int32
	Monthly   float64
}

// Ref is a stable "kind/namespace/name" key.
func (w WorkloadCost) Ref() string { return w.Kind + "/" + w.Namespace + "/" + w.Name }

// CostWorkloads prices each workload's resource requests (sum of containers × replicas).
func CostWorkloads(ws []model.Workload, price model.ResourcePrice) []WorkloadCost {
	out := make([]WorkloadCost, 0, len(ws))
	for _, w := range ws {
		var perReplica float64
		for _, c := range w.Containers {
			perReplica += costmodel.MonthlyCost(model.ResourceSpec{Requests: c.Requests}, price)
		}
		replicas := w.Replicas
		if replicas <= 0 {
			replicas = 1
		}
		out = append(out, WorkloadCost{
			Kind: string(w.Kind), Namespace: w.Namespace, Name: w.Name,
			Replicas: replicas, Monthly: perReplica * float64(replicas),
		})
	}
	return out
}

// ChangeStatus classifies a workload's cost change between base and head.
type ChangeStatus string

// How a workload's cost changed between base and head.
const (
	Added     ChangeStatus = "added"
	Removed   ChangeStatus = "removed"
	Changed   ChangeStatus = "changed"
	Unchanged ChangeStatus = "unchanged"
)

// CostChange is one workload's before→after monthly cost.
type CostChange struct {
	Ref       string
	Kind      string
	Namespace string
	Name      string
	Before    float64
	After     float64
	Delta     float64 // After - Before (positive = costs more)
	Status    ChangeStatus
}

// CostReport is the result of comparing base vs head manifests.
type CostReport struct {
	Changes     []CostChange
	BeforeTotal float64
	AfterTotal  float64
	NetDelta    float64 // AfterTotal - BeforeTotal
}

// Compare diffs base vs head workload costs by ref and returns a sorted report (largest absolute
// delta first). A nil/empty base means everything is "added" — i.e. the full cost of head.
func Compare(base, head []WorkloadCost) CostReport {
	baseByRef := indexByRef(base)
	headByRef := indexByRef(head)

	var rep CostReport
	seen := map[string]bool{}
	for ref, h := range headByRef {
		seen[ref] = true
		b, ok := baseByRef[ref]
		ch := CostChange{Ref: ref, Kind: h.Kind, Namespace: h.Namespace, Name: h.Name, After: h.Monthly}
		if ok {
			ch.Before = b.Monthly
			ch.Status = Changed
			if approxEqual(b.Monthly, h.Monthly) {
				ch.Status = Unchanged
			}
		} else {
			ch.Status = Added
		}
		ch.Delta = ch.After - ch.Before
		rep.Changes = append(rep.Changes, ch)
	}
	for ref, b := range baseByRef {
		if seen[ref] {
			continue
		}
		rep.Changes = append(rep.Changes, CostChange{
			Ref: ref, Kind: b.Kind, Namespace: b.Namespace, Name: b.Name,
			Before: b.Monthly, After: 0, Delta: -b.Monthly, Status: Removed,
		})
	}

	for _, c := range rep.Changes {
		rep.BeforeTotal += c.Before
		rep.AfterTotal += c.After
	}
	rep.NetDelta = rep.AfterTotal - rep.BeforeTotal

	sort.SliceStable(rep.Changes, func(i, j int) bool {
		if abs(rep.Changes[i].Delta) != abs(rep.Changes[j].Delta) {
			return abs(rep.Changes[i].Delta) > abs(rep.Changes[j].Delta)
		}
		return rep.Changes[i].Ref < rep.Changes[j].Ref
	})
	return rep
}

// DefaultPrice returns the blended config price (optionally overridden), for callers that don't
// have a live cluster to derive node pricing from.
func DefaultPrice(cpuCoreMonth, memGiBMonth float64) model.ResourcePrice {
	cfg := pricing.DefaultConfig()
	if cpuCoreMonth > 0 {
		cfg.CPUCoreMonth = cpuCoreMonth
	}
	if memGiBMonth > 0 {
		cfg.MemGiBMonth = memGiBMonth
	}
	return model.ResourcePrice{CPUCoreMonth: cfg.CPUCoreMonth, MemGiBMonth: cfg.MemGiBMonth, Source: "derived node pricing"}
}

func indexByRef(cs []WorkloadCost) map[string]WorkloadCost {
	m := make(map[string]WorkloadCost, len(cs))
	for _, c := range cs {
		m[c.Ref()] = c
	}
	return m
}

func approxEqual(a, b float64) bool { return abs(a-b) < 0.005 }

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
