// Package scan orchestrates a full scan: discover workloads, gather usage from the best
// available tier, price them, build recommendations, and compute the efficiency score.
package scan

import (
	"context"

	"github.com/kubetidy/kubetidy/internal/costmodel"
	"github.com/kubetidy/kubetidy/internal/model"
	"github.com/kubetidy/kubetidy/internal/pricing"
	"github.com/kubetidy/kubetidy/internal/rightsizer"
	"github.com/kubetidy/kubetidy/internal/score"
	"github.com/kubetidy/kubetidy/internal/usage"
)

// Engine wires the components together. It depends only on interfaces, so it is unit-testable
// with fakes.
type Engine struct {
	Workloads []model.Workload
	Usage     usage.Provider
	Price     pricing.Provider
	Policy    model.Policy
	Context   string
	Namespace string
}

// Run executes the scan and returns a fully populated ScanResult.
//
// For each workload and container: fetch usage (skip/annotate when absent), compute the
// rightsizer proposal and confidence, price the delta, and assemble a Recommendation.
// Finally compute the efficiency score and aggregate total waste.
//
// IMPLEMENTED BY AGENT: see internal/scan task. Use rightsizer.Recommend/Confidence,
// costmodel.MonthlySavings, and score.Compute. Annotate result.Warnings on degradation.
func (e *Engine) Run(ctx context.Context) (model.ScanResult, error) {
	// References kept so the package compiles before implementation; the agent replaces
	// this body.
	_ = ctx
	_ = rightsizer.Recommend
	_ = rightsizer.Confidence
	_ = costmodel.MonthlySavings
	_ = score.Compute
	return model.ScanResult{}, nil
}
