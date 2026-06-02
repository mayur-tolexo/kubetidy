package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kubetidy/kubetidy/internal/model"
)

// fixture builds a representative ScanResult used across tests.
func fixture() model.ScanResult {
	return model.ScanResult{
		Context:       "prod-us-east",
		Namespace:     "",
		Tier:          model.TierHistorical,
		GeneratedAt:   time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
		WorkloadCount: 3,
		Recommendations: []model.Recommendation{
			{
				Workload:       model.Workload{Kind: model.KindDeployment, Name: "search-api", Namespace: "default"},
				ContainerName:  "search-api",
				Current:        model.ResourceSpec{Requests: model.ResourceAmounts{CPUMillicores: 1000, MemoryBytes: 2 * gib}},
				Proposed:       model.ResourceSpec{Requests: model.ResourceAmounts{CPUMillicores: 200, MemoryBytes: 512 * mib}},
				MonthlySavings: 90,
				Confidence:     model.Confidence{Score: 0.80, Reason: "tier 1, 14d"},
				Tier:           model.TierHistorical,
				Evidence:       "P95 cpu 170m, max mem 0.4Gi over 14d",
			},
			{
				Workload:       model.Workload{Kind: model.KindDeployment, Name: "checkout-api", Namespace: "default"},
				ContainerName:  "checkout-api",
				Current:        model.ResourceSpec{Requests: model.ResourceAmounts{CPUMillicores: 2000, MemoryBytes: 4 * gib}},
				Proposed:       model.ResourceSpec{Requests: model.ResourceAmounts{CPUMillicores: 320, MemoryBytes: 1181116006}}, // ~1.1Gi
				MonthlySavings: 210,
				Confidence:     model.Confidence{Score: 0.96, Reason: "tier 1, 14d, low variance"},
				Tier:           model.TierHistorical,
				Usage: model.UsageStats{
					CPUMillicores: model.Percentiles{Avg: 200, P50: 250, P95: 280, P99: 300, Max: 350},
					MemoryBytes:   model.Percentiles{Avg: 700 * mib, P50: 720 * mib, P95: 820 * mib, P99: 870 * mib, Max: 0.9 * gib},
					Window:        14 * 24 * time.Hour, Samples: 1_200_000, Tier: model.TierHistorical,
				},
				Evidence:    "p95 cpu 280m, peak mem 0.9Gi over 14d · 1.2M samples",
				Explanation: []string{"cpu request = P95 280m + 15% headroom = 322m -> 320m", "mem request = max 0.9Gi + 15% headroom"},
			},
		},
		EfficiencyScore:   41,
		TotalMonthlyWaste: 7420,
		Warnings:          []string{"OpenCost unreachable, derived cost from node pricing"},
	}
}

// snapshotFixture mirrors the dangerous Tier-0 case: a single live snapshot with a loud
// safety caveat and a workload that idles at scan time but must not be cut to nothing.
func snapshotFixture() model.ScanResult {
	return model.ScanResult{
		Context:       "neevcloud",
		Tier:          model.TierSnapshot,
		WorkloadCount: 1,
		Recommendations: []model.Recommendation{
			{
				Workload:       model.Workload{Kind: model.KindDeployment, Name: "redis-master", Namespace: "data"},
				ContainerName:  "redis",
				Current:        model.ResourceSpec{Requests: model.ResourceAmounts{CPUMillicores: 500, MemoryBytes: 512 * mib}},
				Proposed:       model.ResourceSpec{Requests: model.ResourceAmounts{CPUMillicores: 100, MemoryBytes: 170 * mib}},
				MonthlySavings: 12,
				Confidence:     model.Confidence{Score: 0.35, Reason: "tier 0, 1 sample"},
				Tier:           model.TierSnapshot,
				Usage: model.UsageStats{
					CPUMillicores: model.Percentiles{Avg: 40, P95: 40, P99: 40, Max: 40},
					MemoryBytes:   model.Percentiles{Avg: 130 * mib, P95: 130 * mib, P99: 130 * mib, Max: 130 * mib},
					Samples:       1, Tier: model.TierSnapshot,
				},
				Evidence: "live cpu 40m, mem 130Mi (single snapshot)",
			},
		},
		EfficiencyScore:   58,
		TotalMonthlyWaste: 12,
		Warnings: []string{
			"Tier 0: recommendations come from a single live snapshot, not historical peaks — apply with caution.",
		},
	}
}

func TestTableBanner(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{Color: false}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()

	wantBanner := "kubetidy · prod-us-east  ·  data: 1 (Prometheus)"
	if !strings.Contains(out, wantBanner) {
		t.Errorf("missing banner line %q\n--- got ---\n%s", wantBanner, out)
	}
	// No ANSI escape codes on the golden path.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("output contains ANSI codes with Color=false\n--- got ---\n%s", out)
	}
}

func TestTableHeroSummary(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{Color: false}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Efficiency  41/100") {
		t.Errorf("missing hero efficiency line\n--- got ---\n%s", out)
	}
	// Color off => no bar characters.
	if strings.Contains(out, barFilled) || strings.Contains(out, barEmpty) {
		t.Errorf("bar must not be drawn when Color=false\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "Waste       $7,420/mo  potential savings") {
		t.Errorf("missing hero waste line\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "2 workloads can be rightsized") {
		t.Errorf("missing one-line takeaway\n--- got ---\n%s", out)
	}
}

func TestTakeawaySingular(t *testing.T) {
	r := fixture()
	r.Recommendations = r.Recommendations[:1]
	var buf bytes.Buffer
	if err := Table(&buf, r, Options{Color: false}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(buf.String(), "1 workload can be rightsized") {
		t.Errorf("singular takeaway wrong\n--- got ---\n%s", buf.String())
	}
}

func TestTableColorDrawsBar(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{Color: true}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	// 41/100 -> round(4.1) = 4 filled cells.
	wantBar := strings.Repeat(barFilled, 4) + strings.Repeat(barEmpty, 6)
	if !strings.Contains(out, wantBar) {
		t.Errorf("expected bar %q in output\n--- got ---\n%s", wantBar, out)
	}
	// Colored output should carry ANSI escapes.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI codes with Color=true\n--- got ---\n%s", out)
	}
}

func TestTableRecommendationRow(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{Color: false, TopN: 1}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()

	// Card heading: workload, location, savings, confidence.
	for _, want := range []string{"▸ checkout-api", "Deployment/default", "save $210/mo", "█ high 96%"} {
		if !strings.Contains(out, want) {
			t.Errorf("card heading missing %q\n--- got ---\n%s", want, out)
		}
	}
	// TopN=1 keeps the top rec (checkout-api, $210) and drops search-api ($90).
	if strings.Contains(out, "search-api") {
		t.Errorf("TopN=1 should truncate lower rec\n--- got ---\n%s", out)
	}
	// The distribution block: header + the p95 cpu value, and the request → proposed change.
	for _, want := range []string{"avg", "p95", "p99", "peak", "request → proposed", "280m", "2000m → 320m", "4Gi → 1.1Gi"} {
		if !strings.Contains(out, want) {
			t.Errorf("card body missing %q\n--- got ---\n%s", want, out)
		}
	}
	// Basis line states the data backing it.
	if !strings.Contains(out, "basis") || !strings.Contains(out, "14d history") {
		t.Errorf("missing basis line\n--- got ---\n%s", out)
	}
}

func TestTableAdaptiveMemoryNeverZeroGi(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, snapshotFixture(), Options{Color: false}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()

	// 512Mi -> 170Mi must render in Mi, never as "0.0Gi".
	if strings.Contains(out, "0.0Gi") {
		t.Errorf("must never print 0.0Gi\n--- got ---\n%s", out)
	}
	// 512Mi (requested) and 170Mi (proposed) render in Mi across the cpu/mem cells.
	if !strings.Contains(out, "512Mi") || !strings.Contains(out, "170Mi") {
		t.Errorf("missing sub-Gi adaptive mem rendering\n--- got ---\n%s", out)
	}
}

func TestTableGrowRecommendationDistinct(t *testing.T) {
	r := fixture()
	// Make search-api a GROW recommendation (negative savings = costs more for reliability).
	r.Recommendations[0].MonthlySavings = -7
	var buf bytes.Buffer
	if err := Table(&buf, r, Options{Color: false}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	// Grow rows are visually distinct: an up marker and a (grow) tag, not a plain saving.
	if !strings.Contains(out, "↑ +$7/mo (grow)") {
		t.Errorf("grow recommendation not shown distinctly\n--- got ---\n%s", out)
	}
}

func TestTableSortingOrderTopNZero(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{Color: false, TopN: 0}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	ci := strings.Index(out, "checkout-api")
	si := strings.Index(out, "search-api")
	if ci < 0 || si < 0 {
		t.Fatalf("both recs should be shown\n--- got ---\n%s", out)
	}
	if ci > si {
		t.Errorf("checkout-api ($210) must sort before search-api ($90)\n--- got ---\n%s", out)
	}
}

func TestTableWarningsProminent(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, snapshotFixture(), Options{}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()

	marker := "⚠  note: Tier 0:"
	wi := strings.Index(out, marker)
	if wi < 0 {
		t.Fatalf("missing prominent warning marker %q\n--- got ---\n%s", marker, out)
	}
	// The warning must appear ABOVE the recommendations, not buried at the bottom.
	ri := strings.Index(out, "redis-master")
	if ri < 0 {
		t.Fatalf("recommendation missing\n--- got ---\n%s", out)
	}
	if wi > ri {
		t.Errorf("warning must appear before recommendations\nwarning@%d rec@%d\n--- got ---\n%s", wi, ri, out)
	}
}

func TestTableLegendShown(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "each card:") {
		t.Errorf("missing legend\n--- got ---\n%s", out)
	}
}

func TestTableEmptyRecommendations(t *testing.T) {
	r := fixture()
	r.Recommendations = nil
	r.TotalMonthlyWaste = 0
	r.Warnings = nil
	var buf bytes.Buffer
	if err := Table(&buf, r, Options{}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "✓ No rightsizing opportunities found — this cluster looks tidy.") {
		t.Errorf("missing empty message\n--- got ---\n%s", out)
	}
	// Score/waste lines are still shown in the empty case.
	if !strings.Contains(out, "Efficiency  41/100") {
		t.Errorf("missing score line in empty case\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "Waste       $0/mo") {
		t.Errorf("missing zero waste in empty case\n--- got ---\n%s", out)
	}
	if strings.Contains(out, "⚠  note:") {
		t.Errorf("notes should be absent with no warnings\n--- got ---\n%s", out)
	}
}

func TestTableExplainMatch(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{Explain: "checkout"}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Deployment/default/checkout-api") {
		t.Errorf("explain should render headline ref\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "how it's sized") {
		t.Errorf("explain should render the sizing derivation\n--- got ---\n%s", out)
	}
	// Should NOT render the summary table.
	if strings.Contains(out, "WORKLOAD") {
		t.Errorf("explain must not render summary table\n--- got ---\n%s", out)
	}
}

func TestTableExplainNoMatch(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{Explain: "nope-nonexistent"}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `no recommendation matches "nope-nonexistent"`) {
		t.Errorf("expected no-match message\n--- got ---\n%s", out)
	}
}

func TestExplain(t *testing.T) {
	rec := fixture().Recommendations[1] // checkout-api
	var buf bytes.Buffer
	if err := Explain(&buf, rec); err != nil {
		t.Fatalf("Explain: %v", err)
	}
	out := buf.String()
	checks := []string{
		"Deployment/default/checkout-api · container: checkout-api",
		"observed usage", // section heading
		"avg", "p95", "p99", "peak",
		"280m",         // p95 cpu in the distribution table
		"2000m → 320m", // cpu change column
		"4Gi → 1.1Gi",  // mem change column
		"savings",      // key-value block
		"$210 / month",
		"high 96%",              // confidence band + %
		"over-allocated on cpu", // verdict
		"how it's sized",
		"cpu request = P95 280m", // from fixture Explanation
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("Explain missing %q\n--- got ---\n%s", c, out)
		}
	}
}

func TestExplainNegativeSavings(t *testing.T) {
	rec := fixture().Recommendations[0]
	rec.MonthlySavings = -45 // under-provisioned: should grow, costs more
	var buf bytes.Buffer
	if err := Explain(&buf, rec); err != nil {
		t.Fatalf("Explain: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "-$45 / month") {
		t.Errorf("expected signed negative savings\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "grow for reliability") {
		t.Errorf("expected grow clarification on negative savings\n--- got ---\n%s", out)
	}
}

func TestJSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, fixture()); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	raw := buf.Bytes()

	// Must be indented.
	if !bytes.Contains(raw, []byte("\n  ")) {
		t.Errorf("JSON output is not indented\n%s", raw)
	}

	var decoded model.ScanResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("JSON does not round-trip: %v\n%s", err, raw)
	}
	if decoded.Context != "prod-us-east" {
		t.Errorf("context not preserved: %q", decoded.Context)
	}
	if decoded.EfficiencyScore != 41 {
		t.Errorf("efficiencyScore not preserved: %d", decoded.EfficiencyScore)
	}
	if len(decoded.Recommendations) != 2 {
		t.Fatalf("recommendations not preserved: %d", len(decoded.Recommendations))
	}
	if decoded.Recommendations[1].MonthlySavings != 210 {
		t.Errorf("monthlySavings not preserved: %v", decoded.Recommendations[1].MonthlySavings)
	}

	// Tier serializes as the integer code.
	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("generic unmarshal: %v", err)
	}
	if tier, ok := generic["Tier"].(float64); !ok || int(tier) != int(model.TierHistorical) {
		t.Errorf("Tier should serialize as integer code %d, got %v", model.TierHistorical, generic["Tier"])
	}
}

func TestJSONStableAcrossCalls(t *testing.T) {
	var a, b bytes.Buffer
	if err := JSON(&a, fixture()); err != nil {
		t.Fatal(err)
	}
	if err := JSON(&b, fixture()); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("JSON output not stable across calls")
	}
}

func TestFormatMem(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0"},
		{512, "512B"},
		{2 * kib, "2Ki"},
		{170 * mib, "170Mi"},
		{512 * mib, "512Mi"},
		{2 * gib, "2Gi"},
		{1181116006, "1.1Gi"}, // ~1.1Gi
		{1536 * mib, "1.5Gi"},
		// Critically: a tiny value never collapses to "0.0Gi".
		{7 * mib, "7Mi"},
		{1, "1B"},
	}
	for _, c := range cases {
		got := formatMem(c.bytes)
		if got != c.want {
			t.Errorf("formatMem(%d) = %q, want %q", c.bytes, got, c.want)
		}
		if strings.Contains(got, "0.0Gi") {
			t.Errorf("formatMem(%d) produced 0.0Gi", c.bytes)
		}
	}
}

func TestHumanizeMemory(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0"},
		{512, "512B"},
		{2 * kib, "2Ki"},
		{512 * mib, "512Mi"},
		{2 * gib, "2Gi"},
		{1181116006, "1.1Gi"},
		{1536 * mib, "1.5Gi"},
	}
	for _, c := range cases {
		if got := humanizeMemory(c.bytes); got != c.want {
			t.Errorf("humanizeMemory(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

func TestGroupThousands(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1,000"},
		{7420, "7,420"},
		{1234567, "1,234,567"},
	}
	for _, c := range cases {
		if got := groupThousands(c.n); got != c.want {
			t.Errorf("groupThousands(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestScoreBar(t *testing.T) {
	cases := []struct {
		score  int
		filled int
	}{
		{0, 0},
		{41, 4},
		{45, 5}, // rounds up at 4.5
		{100, 10},
		{150, 10}, // clamped
		{-5, 0},   // clamped
	}
	for _, c := range cases {
		want := strings.Repeat(barFilled, c.filled) + strings.Repeat(barEmpty, barCells-c.filled)
		if got := scoreBar(c.score); got != want {
			t.Errorf("scoreBar(%d) = %q, want %q", c.score, got, want)
		}
	}
}
