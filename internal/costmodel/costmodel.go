// Package costmodel converts a change in resource requests into a monthly dollar delta. It
// is a PURE package: no I/O, fully deterministic, table-tested.
package costmodel

import (
	"github.com/kubetidy/kubetidy/internal/model"
)

// MonthlySavings returns the monthly dollar difference between the current and proposed
// specs at the given price, multiplied across replicas. Positive means the proposal saves
// money; negative means it costs more (under-provisioned workload that should grow).
//
// Cost is based on requests (what the scheduler reserves), not limits.
//
// IMPLEMENTED BY AGENT: see internal/costmodel task.
func MonthlySavings(current, proposed model.ResourceSpec, price model.ResourcePrice, replicas int32) float64 {
	_ = current
	_ = proposed
	_ = price
	_ = replicas
	return 0
}

// MonthlyCost returns the monthly dollar cost of a single replica's requests at the given
// price.
//
// IMPLEMENTED BY AGENT.
func MonthlyCost(spec model.ResourceSpec, price model.ResourcePrice) float64 {
	_ = spec
	_ = price
	return 0
}
