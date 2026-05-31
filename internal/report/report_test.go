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
				Evidence:       "P95 cpu 280m, max mem 0.9Gi over 14d · 1.2M samples",
				Explanation:    []string{"cpu request = P95 280m + 15% headroom = 322m -> 320m", "mem request = max 0.9Gi + 15% headroom"},
			},
		},
		EfficiencyScore:   41,
		TotalMonthlyWaste: 7420,
		Warnings:          []string{"OpenCost unreachable, derived cost from node pricing"},
	}
}

func TestTableScoreAndWasteFormatting(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{Color: false}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()

	wantHeader := "kubetidy scan  ·  context: prod-us-east  ·  tier: 1 (Prometheus)"
	if !strings.Contains(out, wantHeader) {
		t.Errorf("missing header line %q\n--- got ---\n%s", wantHeader, out)
	}
	if !strings.Contains(out, "Cluster Efficiency Score:  41 / 100") {
		t.Errorf("missing score line\n--- got ---\n%s", out)
	}
	// Color off => no bar characters.
	if strings.Contains(out, barFilled) || strings.Contains(out, barEmpty) {
		t.Errorf("bar must not be drawn when Color=false\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "Rightsizing waste:  $7,420 / month") {
		t.Errorf("missing thousands-separated waste\n--- got ---\n%s", out)
	}
	// No ANSI escape codes on the golden path.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("output contains ANSI codes with Color=false\n--- got ---\n%s", out)
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
}

func TestTableTopNTruncationAndSorting(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{Color: false, TopN: 1}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	// Highest savings (checkout-api, $210) must appear; search-api ($90) truncated.
	if !strings.Contains(out, "checkout-api") {
		t.Errorf("top rec missing\n--- got ---\n%s", out)
	}
	if strings.Contains(out, "search-api") {
		t.Errorf("TopN=1 should truncate lower rec\n--- got ---\n%s", out)
	}
	// The shown rec line format.
	if !strings.Contains(out, "cpu 2000m→320m") {
		t.Errorf("missing cpu transition\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "-$210/mo") {
		t.Errorf("missing savings\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "conf 96%") {
		t.Errorf("missing confidence\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "evidence: P95 cpu 280m") {
		t.Errorf("missing evidence line\n--- got ---\n%s", out)
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

func TestTableWarnings(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, fixture(), Options{}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "notes:") {
		t.Errorf("missing notes heading\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "- OpenCost unreachable") {
		t.Errorf("missing warning\n--- got ---\n%s", out)
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
	if !strings.Contains(out, "No rightsizing recommendations.") {
		t.Errorf("missing empty message\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "Rightsizing waste:  $0 / month") {
		t.Errorf("missing zero waste\n--- got ---\n%s", out)
	}
	if strings.Contains(out, "notes:") {
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
	if !strings.Contains(out, "derivation:") {
		t.Errorf("explain should render derivation\n--- got ---\n%s", out)
	}
	// Should NOT render the summary table.
	if strings.Contains(out, "TOP RECOMMENDATIONS") {
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
		"Deployment/default/checkout-api  ·  container: checkout-api",
		"cpu:  2000m → 320m",
		"savings:  $210 / month",
		"confidence:  96% (tier 1, 14d, low variance)",
		"tier:  1 (Prometheus)",
		"evidence:  P95 cpu 280m",
		"derivation:",
		"cpu request = P95 280m",
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
	if !strings.Contains(buf.String(), "savings:  -$45 / month") {
		t.Errorf("expected signed negative savings\n--- got ---\n%s", buf.String())
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
