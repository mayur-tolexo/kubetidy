// Package report renders a ScanResult for humans (TTY table) and machines (JSON), plus the
// --explain detail view.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

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

const (
	barFilled = "▇"
	barEmpty  = "░"
	barCells  = 10
)

// Table renders the human-facing summary (score, dollar waste, top recommendations).
//
// Layout follows design spec §9. Output is deterministic plain UTF-8 when opts.Color is
// false: the efficiency bar is only drawn when opts.Color is true (TTY), so golden tests
// stay stable. Cover with golden-file tests.
func Table(w io.Writer, result model.ScanResult, opts Options) error {
	// --explain short-circuits the summary: render a single matching recommendation.
	if opts.Explain != "" {
		rec, ok := findRecommendation(result, opts.Explain)
		if !ok {
			_, err := fmt.Fprintf(w, "no recommendation matches %q\n", opts.Explain)
			return err
		}
		return Explain(w, rec)
	}

	var b strings.Builder

	fmt.Fprintf(&b, "kubetidy scan  ·  context: %s  ·  tier: %s\n", result.Context, result.Tier.String())
	b.WriteString("\n")

	if opts.Color {
		fmt.Fprintf(&b, "  Cluster Efficiency Score:  %d / 100   %s\n", result.EfficiencyScore, scoreBar(result.EfficiencyScore))
	} else {
		fmt.Fprintf(&b, "  Cluster Efficiency Score:  %d / 100\n", result.EfficiencyScore)
	}
	fmt.Fprintf(&b, "  Rightsizing waste:  %s / month\n", formatDollars(result.TotalMonthlyWaste))

	recs := topRecommendations(result.Recommendations, opts.TopN)
	if len(recs) > 0 {
		b.WriteString("\n")
		b.WriteString("  TOP RECOMMENDATIONS\n")
		b.WriteString("  ─────────────────────────────────────────────────────────\n")

		// Use a tabwriter so the recommendation columns align.
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, rec := range recs {
			_, _ = fmt.Fprintf(tw, "  %s\tcpu %dm→%dm\tmem %s→%s\t-%s/mo\tconf %d%%\n",
				workloadLabel(rec),
				rec.Current.Requests.CPUMillicores,
				rec.Proposed.Requests.CPUMillicores,
				humanizeMemory(rec.Current.Requests.MemoryBytes),
				humanizeMemory(rec.Proposed.Requests.MemoryBytes),
				formatDollarsAbs(rec.MonthlySavings),
				rec.Confidence.Percent(),
			)
			// evidence on its own (non-tabbed) line, flushed after the row.
			// Flushing to an in-memory strings.Builder cannot fail.
			_ = tw.Flush()
			if rec.Evidence != "" {
				fmt.Fprintf(&b, "    evidence: %s\n", rec.Evidence)
			}
		}
	} else {
		b.WriteString("\n")
		b.WriteString("  No rightsizing recommendations.\n")
	}

	if len(result.Warnings) > 0 {
		b.WriteString("\n")
		b.WriteString("  notes:\n")
		for _, warn := range result.Warnings {
			fmt.Fprintf(&b, "    - %s\n", warn)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// JSON renders a stable machine-readable schema of the scan result.
//
// The schema is exactly the JSON encoding of model.ScanResult: an object with
// context/namespace/tier (tier is the integer EvidenceTier code), generatedAt (RFC3339),
// workloadCount, recommendations (array of {workload, containerName, current, proposed,
// monthlySavings, confidence, tier, evidence, explanation}), efficiencyScore,
// scoreBreakdown, totalMonthlyWaste, and warnings. Output is indented for readability and
// is stable for a given input (Go struct field order is deterministic).
func JSON(w io.Writer, result model.ScanResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// Explain renders the full derivation for a single recommendation: a headline (workload,
// container, current vs proposed cpu/mem, savings, confidence with its reason, tier,
// evidence) followed by the recorded Explanation lines.
func Explain(w io.Writer, rec model.Recommendation) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%s  ·  container: %s\n", rec.Workload.Ref(), rec.ContainerName)
	b.WriteString("\n")
	fmt.Fprintf(&b, "  cpu:  %dm → %dm\n", rec.Current.Requests.CPUMillicores, rec.Proposed.Requests.CPUMillicores)
	fmt.Fprintf(&b, "  mem:  %s → %s\n", humanizeMemory(rec.Current.Requests.MemoryBytes), humanizeMemory(rec.Proposed.Requests.MemoryBytes))
	fmt.Fprintf(&b, "  savings:  %s / month\n", formatSignedDollars(rec.MonthlySavings))
	fmt.Fprintf(&b, "  confidence:  %d%% (%s)\n", rec.Confidence.Percent(), rec.Confidence.Reason)
	fmt.Fprintf(&b, "  tier:  %s\n", rec.Tier.String())
	if rec.Evidence != "" {
		fmt.Fprintf(&b, "  evidence:  %s\n", rec.Evidence)
	}

	if len(rec.Explanation) > 0 {
		b.WriteString("\n")
		b.WriteString("  derivation:\n")
		for _, line := range rec.Explanation {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// findRecommendation returns the first recommendation whose container name or workload
// Ref/Name contains the query string.
func findRecommendation(result model.ScanResult, query string) (model.Recommendation, bool) {
	for _, rec := range result.Recommendations {
		if strings.Contains(rec.ContainerName, query) ||
			strings.Contains(rec.Workload.Name, query) ||
			strings.Contains(rec.Workload.Ref(), query) {
			return rec, true
		}
	}
	return model.Recommendation{}, false
}

// topRecommendations returns up to n recommendations sorted by MonthlySavings descending
// (ties broken by workload label for stability). n == 0 means "all".
func topRecommendations(recs []model.Recommendation, n int) []model.Recommendation {
	out := make([]model.Recommendation, len(recs))
	copy(out, recs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MonthlySavings != out[j].MonthlySavings {
			return out[i].MonthlySavings > out[j].MonthlySavings
		}
		return workloadLabel(out[i]) < workloadLabel(out[j])
	})
	if n > 0 && n < len(out) {
		out = out[:n]
	}
	return out
}

// workloadLabel is the short display name for a recommendation's workload.
func workloadLabel(rec model.Recommendation) string {
	if rec.Workload.Name != "" {
		return rec.Workload.Name
	}
	return rec.ContainerName
}

// scoreBar renders a 10-cell ▇/░ bar proportional to score/100.
func scoreBar(score int) string {
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	filled := (score*barCells + 50) / 100 // round to nearest cell
	if filled > barCells {
		filled = barCells
	}
	return strings.Repeat(barFilled, filled) + strings.Repeat(barEmpty, barCells-filled)
}

// formatDollars renders a rounded, thousands-separated dollar amount like "$7,420".
func formatDollars(v float64) string {
	return "$" + groupThousands(roundToInt(v))
}

// formatDollarsAbs renders the absolute value (for "-$X/mo" where the sign is literal).
func formatDollarsAbs(v float64) string {
	n := roundToInt(v)
	if n < 0 {
		n = -n
	}
	return "$" + groupThousands(n)
}

// formatSignedDollars keeps the sign so a negative-savings (grow) rec reads "-$X".
func formatSignedDollars(v float64) string {
	n := roundToInt(v)
	if n < 0 {
		return "-$" + groupThousands(-n)
	}
	return "$" + groupThousands(n)
}

// roundToInt rounds half away from zero.
func roundToInt(v float64) int64 {
	if v < 0 {
		return -int64(-v + 0.5)
	}
	return int64(v + 0.5)
}

// groupThousands inserts comma separators into a non-negative integer's decimal string.
func groupThousands(n int64) string {
	if n < 0 {
		return "-" + groupThousands(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}

const (
	kib = 1024
	mib = 1024 * kib
	gib = 1024 * mib
)

// humanizeMemory renders a byte count in Ki/Mi/Gi, dropping a trailing ".0".
func humanizeMemory(bytes int64) string {
	if bytes == 0 {
		return "0"
	}
	switch {
	case bytes >= gib:
		return trimFloat(float64(bytes)/float64(gib)) + "Gi"
	case bytes >= mib:
		return trimFloat(float64(bytes)/float64(mib)) + "Mi"
	case bytes >= kib:
		return trimFloat(float64(bytes)/float64(kib)) + "Ki"
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// trimFloat formats a float with one decimal place, removing a trailing ".0".
func trimFloat(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	s = strings.TrimSuffix(s, ".0")
	return s
}
