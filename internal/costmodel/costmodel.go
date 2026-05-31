package costmodel

import "github.com/kubetidy/kubetidy/internal/model"

// bytesPerGiB is the number of bytes in one binary gibibyte (1024^3). Memory
// prices are denominated per GiB, so usage in bytes is divided by this factor.
const bytesPerGiB = 1024 * 1024 * 1024

// millicoresPerCore is the number of millicores in one CPU core. CPU prices are
// denominated per core, so millicore requests are divided by this factor.
const millicoresPerCore = 1000

// nonNegative clamps negative prices to zero. Normal flow assumes non-negative
// prices; this guard prevents a malformed price from producing a negative cost.
func nonNegative(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// MonthlyCost returns the dollar cost per month for a single replica's resource
// requests at the given prices.
//
// Cost is based on requests only (what the scheduler reserves), not limits:
//
//	cost = (Requests.CPUMillicores/1000)*CPUCoreMonth
//	     + (Requests.MemoryBytes/1024^3)*MemGiBMonth
//
// Negative prices are treated as zero.
func MonthlyCost(spec model.ResourceSpec, price model.ResourcePrice) float64 {
	cpuCores := float64(spec.Requests.CPUMillicores) / millicoresPerCore
	memGiB := float64(spec.Requests.MemoryBytes) / bytesPerGiB
	return cpuCores*nonNegative(price.CPUCoreMonth) + memGiB*nonNegative(price.MemGiBMonth)
}

// MonthlySavings returns the total monthly dollar delta from moving every
// replica from current to proposed requests. Positive means savings (the
// proposal is cheaper); negative means the proposal costs more (e.g. growing an
// under-provisioned workload).
//
// replicas <= 0 is treated as 1: a workload always has at least one replica
// conceptually, so cost/savings are reported per that single replica rather
// than collapsing to zero.
func MonthlySavings(current, proposed model.ResourceSpec, price model.ResourcePrice, replicas int32) float64 {
	if replicas <= 0 {
		replicas = 1
	}
	perReplica := MonthlyCost(current, price) - MonthlyCost(proposed, price)
	return perReplica * float64(replicas)
}
