// Package rightsizer turns observed usage into recommended resources. It is a PURE package:
// no I/O, fully deterministic, exhaustively table-tested.
package rightsizer

import (
	"github.com/kubetidy/kubetidy/internal/model"
)

// Recommend computes the proposed ResourceSpec for a container given its current spec, its
// observed usage, and the policy. It is the heart of kubetidy and must be deterministic.
//
// Defaults (see design spec §6):
//   - CPU request    = P95 * (1 + CPUHeadroom); no CPU limit unless policy.SetCPULimit.
//   - Memory request = Max * (1 + MemoryHeadroom); memory limit = request when
//     policy.MemoryLimitEqualsRequest, else carried over from current.
//
// IMPLEMENTED BY AGENT: see internal/rightsizer task.
func Recommend(current model.ResourceSpec, usage model.UsageStats, policy model.Policy) model.ResourceSpec {
	_ = current
	_ = usage
	_ = policy
	return model.ResourceSpec{}
}

// Confidence derives a reproducible confidence score from the usage statistics (tier,
// window, sample count, variance). See design spec §7.
//
// IMPLEMENTED BY AGENT.
func Confidence(usage model.UsageStats) model.Confidence {
	_ = usage
	return model.Confidence{}
}
