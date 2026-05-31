// Package score computes the Cluster Efficiency Score (0..100) from a scan result. It is a
// PURE package: no I/O, deterministic, table-tested.
package score

import (
	"github.com/kubetidy/kubetidy/internal/model"
)

// Compute returns the efficiency score (0..100, higher is better) and the explainable
// breakdown of factors that produced it. A cluster whose requests closely match recommended
// resources scores high; large gaps and missing requests/limits score low.
//
// IMPLEMENTED BY AGENT: see internal/score task.
func Compute(result model.ScanResult) (int, []model.ScoreFactor) {
	_ = result
	return 0, nil
}
