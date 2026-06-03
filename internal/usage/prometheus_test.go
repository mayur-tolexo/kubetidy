package usage

import (
	"strings"
	"testing"
	"time"

	prommodel "github.com/prometheus/common/model"

	"github.com/kubetidy/kubetidy/internal/model"
)

func TestParseWindow(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"14d", 14 * 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"90s", 90 * time.Second, false},
		{"  7d  ", 7 * 24 * time.Hour, false},
		{"", 0, true},
		{"abc", 0, true},
		{"0d", 0, true},
		{"-5d", 0, true},
		{"0s", 0, true},
		{"5x", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseWindow(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseWindow(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWindow(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parseWindow(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestPodRegex(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"checkout-api", "^checkout-api-.*"},
		{"web", "^web-.*"},
		// regex metacharacters in the name must be escaped.
		{"a.b+c", `^a\.b\+c-.*`},
	}
	for _, tt := range tests {
		if got := podRegex(tt.name); got != tt.want {
			t.Errorf("podRegex(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestPromQLEscape(t *testing.T) {
	// A workload name with a dot (e.g. a CSI driver) produces a QuoteMeta regex with `\.`, which
	// is invalid inside a PromQL double-quoted string until its backslashes are doubled.
	in := podRegex("rbd.csi.ceph.com-nodeplugin")
	got := promQLEscape(in)
	want := `^rbd\\.csi\\.ceph\\.com-nodeplugin-.*`
	if got != want {
		t.Errorf("promQLEscape(%q) = %q, want %q", in, got, want)
	}
	// The escaped form must not contain a lone backslash-dot (the sequence PromQL rejects).
	if strings.Contains(strings.ReplaceAll(got, `\\`, ""), `\.`) {
		t.Errorf("escaped regex still contains an invalid lone \\. : %q", got)
	}
	// Names without metacharacters are unchanged.
	if got := promQLEscape("^web-.*"); got != "^web-.*" {
		t.Errorf("promQLEscape(plain) = %q, want unchanged", got)
	}
}

func TestCPUQueryBuilder(t *testing.T) {
	q := cpuQuery(0.95, "shop", "^checkout-.*", "14d")
	for _, want := range []string{
		"quantile_over_time(0.95,",
		"container_cpu_usage_seconds_total",
		`namespace="shop"`,
		`pod=~"^checkout-.*"`,
		`container!=""`,
		`container!="POD"`,
		"[5m])[14d:5m]",
		"* 1000",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("cpuQuery missing %q in:\n%s", want, q)
		}
	}
}

func TestMemQueryBuilder(t *testing.T) {
	q := memQuery(0.5, "shop", "^checkout-.*", "7d")
	for _, want := range []string{
		"quantile_over_time(0.5,",
		"container_memory_working_set_bytes",
		`namespace="shop"`,
		`pod=~"^checkout-.*"`,
		"[7d]",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("memQuery missing %q in:\n%s", want, q)
		}
	}
	if strings.Contains(q, "* 1000") {
		t.Errorf("memQuery should not scale memory by 1000:\n%s", q)
	}

	mx := memMaxQuery("shop", "^checkout-.*", "7d")
	if !strings.Contains(mx, "max_over_time(container_memory_working_set_bytes") {
		t.Errorf("memMaxQuery wrong shape:\n%s", mx)
	}
}

// sample builds a prommodel vector sample with a container label.
func sample(container string, v float64) *prommodel.Sample {
	return &prommodel.Sample{
		Metric: prommodel.Metric{"container": prommodel.LabelValue(container)},
		Value:  prommodel.SampleValue(v),
	}
}

func TestMergeResults(t *testing.T) {
	window := 14 * 24 * time.Hour

	cpuP50 := prommodel.Vector{sample("api", 100), sample("sidecar", 10)}
	cpuP95 := prommodel.Vector{sample("api", 280), sample("sidecar", 25)}
	cpuMax := prommodel.Vector{sample("api", 350), sample("sidecar", 40)}
	memP50 := prommodel.Vector{sample("api", 500e6)}
	memP95 := prommodel.Vector{sample("api", 800e6)}
	memMax := prommodel.Vector{sample("api", 900e6)}

	got := mergeResults(window, results{cpuP50: cpuP50, cpuP95: cpuP95, cpuMax: cpuMax, memP50: memP50, memP95: memP95, memMax: memMax})

	api, ok := got["api"]
	if !ok {
		t.Fatal("missing api container")
	}
	if api.Tier != model.TierHistorical {
		t.Errorf("api Tier = %v, want %v", api.Tier, model.TierHistorical)
	}
	if api.Window != window {
		t.Errorf("api Window = %v, want %v", api.Window, window)
	}
	wantCPU := model.Percentiles{P50: 100, P95: 280, Max: 350}
	if api.CPUMillicores != wantCPU {
		t.Errorf("api CPU = %+v, want %+v", api.CPUMillicores, wantCPU)
	}
	wantMem := model.Percentiles{P50: 500e6, P95: 800e6, Max: 900e6}
	if api.MemoryBytes != wantMem {
		t.Errorf("api Mem = %+v, want %+v", api.MemoryBytes, wantMem)
	}
	// Samples should be a positive scrape estimate (window / 5m = 4032).
	wantSamples := int64(window / (5 * time.Minute))
	if api.Samples != wantSamples {
		t.Errorf("api Samples = %d, want %d", api.Samples, wantSamples)
	}

	// sidecar appeared only in the CPU vectors; its memory should be zero, not missing.
	side, ok := got["sidecar"]
	if !ok {
		t.Fatal("missing sidecar container")
	}
	if side.MemoryBytes != (model.Percentiles{}) {
		t.Errorf("sidecar Mem = %+v, want zero", side.MemoryBytes)
	}
	if side.CPUMillicores.P95 != 25 {
		t.Errorf("sidecar CPU P95 = %v, want 25", side.CPUMillicores.P95)
	}
}

func TestMergeResultsSkipsEmptyContainerLabel(t *testing.T) {
	// A series without a container label (or container="") must be ignored.
	vec := prommodel.Vector{
		{Metric: prommodel.Metric{}, Value: 999},
		sample("", 42),
		sample("real", 7),
	}
	got := mergeResults(time.Hour, results{cpuP50: vec})
	if len(got) != 1 {
		t.Fatalf("got %d containers, want 1: %+v", len(got), got)
	}
	if _, ok := got["real"]; !ok {
		t.Errorf("expected only 'real' container, got %+v", got)
	}
}

func TestNewPrometheusProviderValidatesWindow(t *testing.T) {
	if _, err := NewPrometheusProvider("http://localhost:9090", "notaduration"); err == nil {
		t.Error("expected error for invalid window")
	}
	p, err := NewPrometheusProvider("http://localhost:9090", "14d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "prometheus" {
		t.Errorf("Name() = %q, want prometheus", p.Name())
	}
	if p.Tier() != model.TierHistorical {
		t.Errorf("Tier() = %v, want %v", p.Tier(), model.TierHistorical)
	}
}
