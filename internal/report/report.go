// Package report renders a ScanResult for humans (TTY table) and machines (JSON), plus the
// --explain detail view.
//
// The terminal UX is built to be honest about confidence: it surfaces the data tier in the
// banner, shows safety warnings (especially the Tier-0 single-snapshot caveat) prominently
// near the top rather than buried at the bottom, and humanizes memory adaptively so it never
// prints a meaningless "0.0Gi". Output is deterministic plain UTF-8 when opts.Color is false
// (no ANSI escapes, no proportional bar), so golden tests stay stable; ANSI color is emitted
// only when opts.Color is true.
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

// ANSI escape codes used only when Color is enabled. Kept as raw strings (go.mod is frozen,
// so no color library) and never emitted on the deterministic golden path.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

// Table renders the human-facing summary: a context banner, a hero block (efficiency score,
// dollar waste, one-line takeaway), any safety warnings shown prominently, the ranked
// recommendations table, and a legend footer.
//
// Output is deterministic plain UTF-8 when opts.Color is false: the efficiency bar and all
// ANSI escapes are only emitted when opts.Color is true (TTY), so golden tests stay stable.
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

	writeBanner(&b, result, opts)
	b.WriteString("\n")
	writeHero(&b, result, opts)

	// Warnings go HERE — above the recommendations — so a developer sees the safety caveat
	// (e.g. the Tier-0 single-snapshot warning) before acting on any numbers.
	if len(result.Warnings) > 0 {
		b.WriteString("\n")
		writeWarnings(&b, result.Warnings, opts)
	}

	recs := topRecommendations(result.Recommendations, opts.TopN)
	if len(recs) > 0 {
		b.WriteString("\n")
		writeRecommendations(&b, recs, opts)
		b.WriteString("\n")
		writeLegend(&b, opts)
	} else {
		b.WriteString("\n")
		b.WriteString("  ✓ No rightsizing opportunities found — this cluster looks tidy.\n")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// writeBanner emits the top context line, e.g.
// "kubetidy · prod-us-east  ·  data: 1 (Prometheus)".
func writeBanner(b *strings.Builder, result model.ScanResult, opts Options) {
	ctx := result.Context
	if ctx == "" {
		ctx = "(no context)"
	}
	banner := fmt.Sprintf("kubetidy · %s  ·  data: %s", ctx, result.Tier.String())
	if opts.Color {
		fmt.Fprintf(b, "%s%s%s\n", ansiBold+ansiCyan, banner, ansiReset)
	} else {
		b.WriteString(banner + "\n")
	}
}

// writeHero emits the hero summary: efficiency score (with a bar when colored), the dollar
// waste, and a one-line takeaway counting the rightsizing opportunities.
func writeHero(b *strings.Builder, result model.ScanResult, opts Options) {
	if opts.Color {
		fmt.Fprintf(b, "  Efficiency  %d/100  %s\n", result.EfficiencyScore, scoreBar(result.EfficiencyScore))
	} else {
		fmt.Fprintf(b, "  Efficiency  %d/100\n", result.EfficiencyScore)
	}

	waste := formatDollars(result.TotalMonthlyWaste)
	if opts.Color {
		fmt.Fprintf(b, "  Waste       %s%s/mo%s  potential savings\n", ansiBold, waste, ansiReset)
	} else {
		fmt.Fprintf(b, "  Waste       %s/mo  potential savings\n", waste)
	}

	b.WriteString("  " + takeaway(result) + "\n")
}

// takeaway is the one-line human summary under the hero numbers.
func takeaway(result model.ScanResult) string {
	n := len(result.Recommendations)
	switch n {
	case 0:
		return "no workloads need rightsizing"
	case 1:
		return "1 workload can be rightsized"
	default:
		return fmt.Sprintf("%d workloads can be rightsized", n)
	}
}

// writeWarnings emits the prominent note block. Each warning is prefixed with a clear marker
// so the safety caveat cannot be missed.
func writeWarnings(b *strings.Builder, warnings []string, opts Options) {
	for _, warn := range warnings {
		if opts.Color {
			fmt.Fprintf(b, "  %s⚠  note:%s %s\n", ansiYellow+ansiBold, ansiReset, warn)
		} else {
			fmt.Fprintf(b, "  ⚠  note: %s\n", warn)
		}
	}
}

// writeRecommendations emits the aligned recommendations table with a header row and an
// indented evidence line under each recommendation.
func writeRecommendations(b *strings.Builder, recs []model.Recommendation, opts Options) {
	// Render header + all rows through a SINGLE tabwriter so columns align across the whole
	// table, then splice each evidence line in under its row afterward (evidence lines must
	// not participate in column-width calculation).
	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	// Flushing to an in-memory strings.Builder cannot fail.
	_, _ = fmt.Fprintf(tw, "  WORKLOAD\tCPU\tMEM\tSAVINGS\tCONF\n")
	for _, rec := range recs {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n",
			workloadLabel(rec),
			cpuTransition(rec),
			memTransition(rec),
			savingsCell(rec),
			confidenceCell(rec),
		)
	}
	_ = tw.Flush()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) > 0 {
		if opts.Color {
			b.WriteString(ansiBold + lines[0] + ansiReset + "\n")
		} else {
			b.WriteString(lines[0] + "\n")
		}
	}
	for i, rec := range recs {
		if i+1 < len(lines) {
			b.WriteString(lines[i+1] + "\n")
		}
		if rec.Evidence != "" {
			if opts.Color {
				fmt.Fprintf(b, "    %s└ %s%s\n", ansiDim, rec.Evidence, ansiReset)
			} else {
				fmt.Fprintf(b, "    └ %s\n", rec.Evidence)
			}
		}
	}
}

// writeLegend emits the short column/confidence legend footer.
func writeLegend(b *strings.Builder, opts Options) {
	lines := []string{
		"CPU/MEM = current → proposed request · SAVINGS = $/mo (↑ grow = reliability, not a saving)",
		"CONF = confidence: ▒ low · ▓ med · █ high — grows with data history (tier, window, samples). --explain shows the %.",
	}
	for _, l := range lines {
		if opts.Color {
			fmt.Fprintf(b, "  %s%s%s\n", ansiDim, l, ansiReset)
		} else {
			b.WriteString("  " + l + "\n")
		}
	}
}

// cpuTransition renders the CPU request change, e.g. "2000m → 320m".
func cpuTransition(rec model.Recommendation) string {
	return fmt.Sprintf("%dm → %dm", rec.Current.Requests.CPUMillicores, rec.Proposed.Requests.CPUMillicores)
}

// memTransition renders the memory request change with adaptive Mi/Gi units, e.g.
// "4Gi → 1.1Gi" or "512Mi → 170Mi".
func memTransition(rec model.Recommendation) string {
	return fmt.Sprintf("%s → %s",
		formatMem(rec.Current.Requests.MemoryBytes),
		formatMem(rec.Proposed.Requests.MemoryBytes),
	)
}

// savingsCell renders the SAVINGS column. A positive saving reads "$210/mo". A negative
// saving means the workload is under-provisioned and should GROW for reliability, so it is
// shown distinctly with an up marker and a "(grow)" tag rather than as a saving.
func savingsCell(rec model.Recommendation) string {
	if rec.MonthlySavings < 0 {
		return fmt.Sprintf("↑ +%s/mo (grow)", formatDollarsAbs(rec.MonthlySavings))
	}
	return fmt.Sprintf("%s/mo", formatDollarsAbs(rec.MonthlySavings))
}

// confidenceCell renders the qualitative confidence band with a shaded glyph that reads as a
// fill level even without color. A precise percentage is intentionally omitted here (it implies
// false precision); the number is available under --explain.
func confidenceCell(rec model.Recommendation) string {
	switch rec.Confidence.Band() {
	case model.ConfidenceHigh:
		return "█ high"
	case model.ConfidenceMedium:
		return "▓ med"
	default:
		return "▒ low"
	}
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

// Explain renders the full derivation for a single recommendation in clear sections:
// workload + container headline, the cpu/mem change, savings, confidence with its reason,
// the data tier, the evidence, and finally the recorded derivation lines.
func Explain(w io.Writer, rec model.Recommendation) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%s  ·  container: %s\n", rec.Workload.Ref(), rec.ContainerName)
	b.WriteString("\n")
	fmt.Fprintf(&b, "  change:\n")
	fmt.Fprintf(&b, "    cpu:  %dm → %dm\n", rec.Current.Requests.CPUMillicores, rec.Proposed.Requests.CPUMillicores)
	fmt.Fprintf(&b, "    mem:  %s → %s\n", formatMem(rec.Current.Requests.MemoryBytes), formatMem(rec.Proposed.Requests.MemoryBytes))
	b.WriteString("\n")
	if rec.MonthlySavings < 0 {
		fmt.Fprintf(&b, "  savings:  %s / month  (grow for reliability — costs more, not a saving)\n", formatSignedDollars(rec.MonthlySavings))
	} else {
		fmt.Fprintf(&b, "  savings:  %s / month\n", formatSignedDollars(rec.MonthlySavings))
	}
	fmt.Fprintf(&b, "  confidence:  %s (%d%%) — %s\n", rec.Confidence.Band(), rec.Confidence.Percent(), rec.Confidence.Reason)
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

// formatDollarsAbs renders the absolute value (for "$X/mo" where the sign is shown
// separately, e.g. as a grow marker).
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

// formatMem renders a byte count for display with ADAPTIVE units:
//   - 0 bytes is "0"
//   - below 1Gi is shown in Mi (rounded, e.g. "170Mi", "512Mi")
//   - 1Gi and above is shown in Gi with one decimal (e.g. "1.1Gi", "4Gi")
//
// It deliberately never prints "0.0Gi": small values stay in Mi so the output is meaningful.
// Sub-Mi values fall back to Ki/B so a tiny floor (e.g. 32Mi) still reads sensibly.
func formatMem(bytes int64) string {
	if bytes == 0 {
		return "0"
	}
	switch {
	case bytes >= gib:
		return trimFloat(float64(bytes)/float64(gib)) + "Gi"
	case bytes >= mib:
		// Whole Mi reads cleanly; round to the nearest Mi to avoid noisy decimals.
		mi := (bytes + mib/2) / mib
		return fmt.Sprintf("%dMi", mi)
	case bytes >= kib:
		return trimFloat(float64(bytes)/float64(kib)) + "Ki"
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// humanizeMemory is retained for callers/tests that want the precise one-decimal rendering
// at every scale (Ki/Mi/Gi). Table output uses formatMem for adaptive units instead.
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
