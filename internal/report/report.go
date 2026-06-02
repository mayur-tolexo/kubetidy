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
	"time"

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
		if len(recs) < len(result.Recommendations) {
			fmt.Fprintf(&b, "  … showing top %d of %d by savings · --top 0 for all\n\n",
				len(recs), len(result.Recommendations))
		}
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
	// Window + typical sample count are uniform across a scan, so state them ONCE here rather
	// than repeating on every row. Exact per-workload samples live in --explain.
	if w := observedWindow(result); w > 0 {
		banner += fmt.Sprintf("  ·  %s history", formatWindow(w))
		if s := typicalSamples(result); s > 0 {
			banner += fmt.Sprintf(", ~%s samples/workload", formatCount(s))
		}
	}
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

// writeRecommendations renders one card per recommendation. A flat table cannot hold the full
// usage distribution (avg/p95/p99/peak for cpu AND mem) plus the request→proposed change; a card
// can, and putting the long workload name on its own line keeps everything aligned and readable.
func writeRecommendations(b *strings.Builder, recs []model.Recommendation, opts Options) {
	for i, rec := range recs {
		writeCard(b, rec, opts)
		if i < len(recs)-1 {
			b.WriteString("\n")
		}
	}
}

// writeCard renders a single recommendation as a self-contained block: a heading (workload,
// savings, confidence), the observed usage distribution beside the request→proposed change, and
// a basis line stating the data it rests on.
//
//	▸ neevai-rackbank-console · Deployment/neevai-system   save $60/mo · ▒ low 34%
//	         avg    p95    p99    peak    request → proposed
//	  cpu    1m     2m     2m     2m      2000m → 10m
//	  mem    142Mi  149Mi  149Mi  149Mi  4Gi → 171Mi
//	  basis  5h history · 201 samples · cpu sized to p95, mem to peak
func writeCard(b *strings.Builder, rec model.Recommendation, opts Options) {
	head := fmt.Sprintf("▸ %s · %s/%s   save %s · %s",
		rec.Workload.Name, rec.Workload.Kind, rec.Workload.Namespace, savingsCell(rec), confidenceCell(rec))
	if opts.Color {
		fmt.Fprintf(b, "%s%s%s\n", ansiBold, head, ansiReset)
	} else {
		b.WriteString(head + "\n")
	}

	st := rec.Usage
	cpuChange := fmt.Sprintf("%dm → %dm", rec.Current.Requests.CPUMillicores, rec.Proposed.Requests.CPUMillicores)
	memChange := fmt.Sprintf("%s → %s", formatMem(rec.Current.Requests.MemoryBytes), formatMem(rec.Proposed.Requests.MemoryBytes))

	var tb strings.Builder
	tw := tabwriter.NewWriter(&tb, 0, 0, 2, ' ', 0)
	if st.Tier == model.TierSnapshot {
		_, _ = fmt.Fprint(tw, "  \tnow\trequest → proposed\n")
		_, _ = fmt.Fprintf(tw, "  cpu\t%s\t%s\n", distCPU(st.CPUMillicores.P95), cpuChange)
		_, _ = fmt.Fprintf(tw, "  mem\t%s\t%s\n", distMem(st.MemoryBytes.Max), memChange)
	} else {
		_, _ = fmt.Fprint(tw, "  \tavg\tp95\tp99\tpeak\trequest → proposed\n")
		c := st.CPUMillicores
		_, _ = fmt.Fprintf(tw, "  cpu\t%s\t%s\t%s\t%s\t%s\n", distCPU(c.Avg), distCPU(c.P95), distCPU(c.P99), distCPU(c.Max), cpuChange)
		m := st.MemoryBytes
		_, _ = fmt.Fprintf(tw, "  mem\t%s\t%s\t%s\t%s\t%s\n", distMem(m.Avg), distMem(m.P95), distMem(m.P99), distMem(m.Max), memChange)
	}
	_ = tw.Flush()
	if opts.Color {
		// Dim the distribution block so the heading + change stand out.
		for _, ln := range strings.Split(strings.TrimRight(tb.String(), "\n"), "\n") {
			fmt.Fprintf(b, "%s%s%s\n", ansiDim, ln, ansiReset)
		}
	} else {
		b.WriteString(tb.String())
	}

	if basis := basisLine(rec); basis != "" {
		if opts.Color {
			fmt.Fprintf(b, "  %sbasis  %s%s\n", ansiDim, basis, ansiReset)
		} else {
			fmt.Fprintf(b, "  basis  %s\n", basis)
		}
	}
}

// basisLine states the data a recommendation rests on and how the proposal was sized. It calls
// out when memory was kept conservative because history is too young to trust the observed peak
// (the proposal sits well above peak) — the OOM-safety the rightsizer applies.
func basisLine(rec model.Recommendation) string {
	st := rec.Usage
	if st.Tier == model.TierSnapshot {
		return "single live snapshot — memory kept conservative (no historical peak, extra OOM safety)"
	}
	parts := []string{}
	if st.Window > 0 {
		parts = append(parts, formatWindow(st.Window)+" history")
	}
	if st.Samples > 0 {
		parts = append(parts, formatCount(st.Samples)+" samples")
	}
	// If proposed memory sits well above the observed peak, the conservative (immature-history)
	// safety buffer is active — say so, so the larger number reads as deliberate, not a bug.
	peak := st.MemoryBytes.Max
	prop := float64(rec.Proposed.Requests.MemoryBytes)
	if peak > 0 && prop > peak*1.30 {
		parts = append(parts, "memory kept conservative (short history — extra OOM safety)")
	} else {
		parts = append(parts, "cpu→p95, mem→peak (+headroom)")
	}
	return strings.Join(parts, " · ")
}

// writeLegend emits the short column/confidence legend footer.
func writeLegend(b *strings.Builder, opts Options) {
	lines := []string{
		"each card: observed usage (avg/p95/p99/peak) for cpu & mem, then the request → proposed change.",
		"sized to: cpu = p95 + headroom, mem = peak + headroom · CONF ▒ low ▓ med █ high + score%, grows with history.",
		"→ run  kubetidy scan --explain <workload>  for the full derivation.",
	}
	for _, l := range lines {
		if opts.Color {
			fmt.Fprintf(b, "  %s%s%s\n", ansiDim, l, ansiReset)
		} else {
			b.WriteString("  " + l + "\n")
		}
	}
}

// savingsCell renders the SAVE column. A positive saving reads "$210/mo". A negative
// saving means the workload is under-provisioned and should GROW for reliability, so it is
// shown distinctly with an up marker and a "(grow)" tag rather than as a saving.
func savingsCell(rec model.Recommendation) string {
	if rec.MonthlySavings < 0 {
		return fmt.Sprintf("↑ +%s/mo (grow)", formatDollarsAbs(rec.MonthlySavings))
	}
	return fmt.Sprintf("%s/mo", formatDollarsAbs(rec.MonthlySavings))
}

// confidenceCell renders the confidence as a shaded band glyph (a fill level readable without
// color) + the band label + the score percent, e.g. "▒ low 31%". The band leads so the column
// scans at a glance; the percent gives the precise score for those who want it.
func confidenceCell(rec model.Recommendation) string {
	var glyph, label string
	switch rec.Confidence.Band() {
	case model.ConfidenceHigh:
		glyph, label = "█", "high"
	case model.ConfidenceMedium:
		glyph, label = "▓", "med"
	default:
		glyph, label = "▒", "low"
	}
	return fmt.Sprintf("%s %s %d%%", glyph, label, rec.Confidence.Percent())
}

// observedWindow returns the longest observation window across the recommendations — the
// data's time span, shown in the banner. Zero (snapshot/static) means "no window".
func observedWindow(result model.ScanResult) time.Duration {
	var longest time.Duration
	for _, r := range result.Recommendations {
		if r.Usage.Window > longest {
			longest = r.Usage.Window
		}
	}
	return longest
}

// typicalSamples returns the median sample count across recommendations — a representative
// "samples per workload" for the banner, robust to the odd multi-pod outlier. 0 when unknown.
func typicalSamples(result model.ScanResult) int64 {
	counts := make([]int64, 0, len(result.Recommendations))
	for _, r := range result.Recommendations {
		if r.Usage.Samples > 0 {
			counts = append(counts, r.Usage.Samples)
		}
	}
	if len(counts) == 0 {
		return 0
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i] < counts[j] })
	return counts[len(counts)/2]
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

	fmt.Fprintf(&b, "%s  ·  container: %s\n\n", rec.Workload.Ref(), rec.ContainerName)

	// The "why" block: requested vs actually-observed vs proposed, then the verdict. This is
	// what earns trust — it shows we cut to fit real usage, not an arbitrary number.
	b.WriteString("  why this recommendation\n")
	fmt.Fprintf(&b, "    requested   cpu %s   ·   mem %s\n",
		cpuMilli(float64(rec.Current.Requests.CPUMillicores)), formatMem(rec.Current.Requests.MemoryBytes))
	writeObserved(&b, rec.Usage)
	fmt.Fprintf(&b, "    proposed    cpu %s   ·   mem %s\n",
		cpuMilli(float64(rec.Proposed.Requests.CPUMillicores)), formatMem(rec.Proposed.Requests.MemoryBytes))
	if v := verdict(rec); v != "" {
		fmt.Fprintf(&b, "    verdict     %s\n", v)
	}

	b.WriteString("\n")
	if rec.MonthlySavings < 0 {
		fmt.Fprintf(&b, "  savings      %s / month  (grow for reliability — costs more, not a saving)\n", formatSignedDollars(rec.MonthlySavings))
	} else {
		fmt.Fprintf(&b, "  savings      %s / month\n", formatSignedDollars(rec.MonthlySavings))
	}
	fmt.Fprintf(&b, "  confidence   %s (%d%%) — %s\n", rec.Confidence.Band(), rec.Confidence.Percent(), rec.Confidence.Reason)
	fmt.Fprintf(&b, "  tier         %s\n", rec.Tier.String())

	if len(rec.Explanation) > 0 {
		b.WriteString("\n  derivation\n")
		for _, line := range rec.Explanation {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// writeObserved renders the observed-usage block. For a single snapshot there is no
// distribution, so it shows the one reading; otherwise an aligned avg/p95/p99/peak table.
func writeObserved(b *strings.Builder, st model.UsageStats) {
	if st.Tier == model.TierSnapshot {
		fmt.Fprintf(b, "    observed    single live snapshot · cpu %s · mem %s\n",
			cpuMilli(st.CPUMillicores.P95), formatMem(int64(st.MemoryBytes.Max+0.5)))
		return
	}
	fmt.Fprintf(b, "    observed    over %s · %s samples\n", formatWindow(st.Window), formatCount(st.Samples))

	var tw strings.Builder
	t := tabwriter.NewWriter(&tw, 0, 0, 2, ' ', 0)
	// Leading empty cells indent the block; the header has one extra so avg/p95/... sit over
	// the values, not over the cpu/mem labels.
	_, _ = fmt.Fprintf(t, "\t\t\tavg\tp95\tp99\tpeak\n")
	c := st.CPUMillicores
	_, _ = fmt.Fprintf(t, "\t\tcpu\t%s\t%s\t%s\t%s\n", distCPU(c.Avg), distCPU(c.P95), distCPU(c.P99), distCPU(c.Max))
	m := st.MemoryBytes
	_, _ = fmt.Fprintf(t, "\t\tmem\t%s\t%s\t%s\t%s\n", distMem(m.Avg), distMem(m.P95), distMem(m.P99), distMem(m.Max))
	_ = t.Flush()
	b.WriteString(tw.String())
}

// verdict summarizes how over- (or under-) provisioned the workload is, as a "Nx" factor of
// current request vs proposed. Empty when neither metric moves meaningfully.
func verdict(rec model.Recommendation) string {
	cpuF := factor(float64(rec.Current.Requests.CPUMillicores), float64(rec.Proposed.Requests.CPUMillicores))
	memF := factor(float64(rec.Current.Requests.MemoryBytes), float64(rec.Proposed.Requests.MemoryBytes))
	var parts []string
	if s := factorPhrase("cpu", cpuF); s != "" {
		parts = append(parts, s)
	}
	if s := factorPhrase("mem", memF); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// factor returns current/proposed (>1 = over-allocated, <1 = under), or 0 when not computable.
func factor(current, proposed float64) float64 {
	if proposed <= 0 || current <= 0 {
		return 0
	}
	return current / proposed
}

func factorPhrase(metric string, f float64) string {
	switch {
	case f >= 1.5:
		return fmt.Sprintf("~%.0f× over-allocated on %s", f, metric)
	case f > 0 && f <= 0.67:
		return fmt.Sprintf("~%.0f× under-provisioned on %s", 1/f, metric)
	default:
		return ""
	}
}

// distCPU/distMem format a distribution stat, rendering "—" for an absent (zero) value so an
// unpopulated avg/p99 (e.g. recorded by an older operator) never reads as a misleading "0m".
func distCPU(v float64) string {
	if v <= 0 {
		return "—"
	}
	return cpuMilli(v)
}
func distMem(v float64) string {
	if v <= 0 {
		return "—"
	}
	return formatMem(int64(v + 0.5))
}

// cpuMilli formats a CPU millicore value: one decimal below 1m (so a tiny average isn't shown
// as "0m"), whole millicores otherwise.
func cpuMilli(v float64) string {
	if v > 0 && v < 0.95 {
		return fmt.Sprintf("%.1fm", v)
	}
	return fmt.Sprintf("%dm", int64(v+0.5))
}

// formatWindow renders an observation window compactly (e.g. "14d", "6h", "33m", "<1m").
func formatWindow(d time.Duration) string {
	switch {
	case d <= 0:
		return "0"
	case d.Hours() >= 24:
		return fmt.Sprintf("%dd", int64(d.Hours()/24+0.5))
	case d.Hours() >= 1:
		return fmt.Sprintf("%dh", int64(d.Hours()+0.5))
	case d.Minutes() >= 1:
		return fmt.Sprintf("%dm", int64(d.Minutes()+0.5))
	default:
		return "<1m"
	}
}

// formatCount renders large sample counts compactly (1200000 -> "1.2M", 9800 -> "9.8k").
func formatCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
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
