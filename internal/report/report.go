// Package report renders a ScanResult for humans (TTY table) and machines (JSON), plus the
// --explain detail view.
package report

import (
	"io"

	"github.com/kubetidy/kubetidy/internal/model"
)

// Options controls rendering.
type Options struct {
	// Color enables ANSI color/bars when the output is a TTY.
	Color bool
	// TopN limits the number of recommendations shown in table output (0 = all).
	TopN int
	// Explain, when non-empty, renders the full derivation for the workload whose Ref or
	// name matches, instead of the summary table.
	Explain string
}

// Table renders the human-facing summary (score, dollar waste, top recommendations).
//
// IMPLEMENTED BY AGENT: see internal/report task. Cover with golden-file tests.
func Table(w io.Writer, result model.ScanResult, opts Options) error {
	_ = w
	_ = result
	_ = opts
	return nil
}

// JSON renders a stable machine-readable schema of the scan result.
//
// IMPLEMENTED BY AGENT.
func JSON(w io.Writer, result model.ScanResult) error {
	_ = w
	_ = result
	return nil
}

// Explain renders the full derivation for a single recommendation.
//
// IMPLEMENTED BY AGENT.
func Explain(w io.Writer, rec model.Recommendation) error {
	_ = w
	_ = rec
	return nil
}
