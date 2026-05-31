package usage

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	prommodel "github.com/prometheus/common/model"

	"github.com/kubetidy/kubetidy/internal/model"
)

// prometheusProvider implements Tier 1 using historical percentiles from Prometheus
// (container_cpu_usage_seconds_total / container_memory_working_set_bytes via cAdvisor +
// kube-state-metrics), over a configurable window.
type prometheusProvider struct {
	api    promv1.API
	window string // e.g. "14d"
}

// NewPrometheusProvider builds a Tier-1 provider from a Prometheus base URL.
func NewPrometheusProvider(baseURL, window string) (Provider, error) {
	if _, err := parseWindow(window); err != nil {
		return nil, fmt.Errorf("invalid window %q: %w", window, err)
	}
	client, err := promapi.NewClient(promapi.Config{Address: baseURL})
	if err != nil {
		return nil, err
	}
	return &prometheusProvider{api: promv1.NewAPI(client), window: window}, nil
}

func (p *prometheusProvider) Name() string             { return "prometheus" }
func (p *prometheusProvider) Tier() model.EvidenceTier { return model.TierHistorical }

// resolution is the inner rate/range step used inside the sub-query.
const resolution = "5m"

// Usage runs percentile queries for CPU and memory per container over the window.
//
// For each metric we issue three instant queries (P50, P95, Max) and merge the results by
// container label. CPU comes from rate(container_cpu_usage_seconds_total) scaled to
// millicores; memory from container_memory_working_set_bytes (the figure the kernel OOM
// killer watches), so we use max-over-window for memory rather than a percentile to stay
// safe against OOMs. Window is the parsed duration; Tier is TierHistorical.
func (p *prometheusProvider) Usage(ctx context.Context, w model.Workload) (map[string]model.UsageStats, error) {
	window, err := parseWindow(p.window)
	if err != nil {
		return nil, fmt.Errorf("invalid window %q: %w", p.window, err)
	}
	regex := podRegex(w.Name)

	cpuP50, err := p.queryVector(ctx, cpuQuery(0.5, w.Namespace, regex, p.window))
	if err != nil {
		return nil, err
	}
	cpuP95, err := p.queryVector(ctx, cpuQuery(0.95, w.Namespace, regex, p.window))
	if err != nil {
		return nil, err
	}
	cpuMax, err := p.queryVector(ctx, cpuMaxQuery(w.Namespace, regex, p.window))
	if err != nil {
		return nil, err
	}
	memP50, err := p.queryVector(ctx, memQuery(0.5, w.Namespace, regex, p.window))
	if err != nil {
		return nil, err
	}
	memP95, err := p.queryVector(ctx, memQuery(0.95, w.Namespace, regex, p.window))
	if err != nil {
		return nil, err
	}
	memMax, err := p.queryVector(ctx, memMaxQuery(w.Namespace, regex, p.window))
	if err != nil {
		return nil, err
	}

	return mergeResults(window, cpuP50, cpuP95, cpuMax, memP50, memP95, memMax), nil
}

// queryVector runs an instant query and returns the resulting vector. Query warnings are
// tolerated (a non-empty result is still returned); only a hard error fails the call.
func (p *prometheusProvider) queryVector(ctx context.Context, query string) (prommodel.Vector, error) {
	val, _, err := p.api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}
	vec, ok := val.(prommodel.Vector)
	if !ok {
		// A non-vector result (e.g. scalar/string) yields no per-container data.
		return prommodel.Vector{}, nil
	}
	return vec, nil
}

// mergeResults groups the six metric vectors by their "container" label into UsageStats.
func mergeResults(window time.Duration, cpuP50, cpuP95, cpuMax, memP50, memP95, memMax prommodel.Vector) map[string]model.UsageStats {
	stats := make(map[string]*model.UsageStats)

	apply := func(vec prommodel.Vector, set func(s *model.UsageStats, v float64)) {
		for _, sample := range vec {
			name := string(sample.Metric["container"])
			if name == "" {
				continue
			}
			s := stats[name]
			if s == nil {
				s = &model.UsageStats{Window: window, Tier: model.TierHistorical}
				stats[name] = s
			}
			v := float64(sample.Value)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				v = 0
			}
			set(s, v)
		}
	}

	apply(cpuP50, func(s *model.UsageStats, v float64) { s.CPUMillicores.P50 = v })
	apply(cpuP95, func(s *model.UsageStats, v float64) { s.CPUMillicores.P95 = v })
	apply(cpuMax, func(s *model.UsageStats, v float64) { s.CPUMillicores.Max = v })
	apply(memP50, func(s *model.UsageStats, v float64) { s.MemoryBytes.P50 = v })
	apply(memP95, func(s *model.UsageStats, v float64) { s.MemoryBytes.P95 = v })
	apply(memMax, func(s *model.UsageStats, v float64) { s.MemoryBytes.Max = v })

	// Samples: estimate the number of scrapes covered by the window at the inner
	// resolution. This is a coarse lower bound (one series, one scrape per resolution
	// step) used by the confidence model, not an exact count.
	res, _ := parseWindow(resolution)
	var samples int64
	if res > 0 {
		samples = int64(window / res)
	}

	out := make(map[string]model.UsageStats, len(stats))
	for name, s := range stats {
		s.Samples = samples
		out[name] = *s
	}
	return out
}

// cpuQuery builds a millicores quantile-over-time query for CPU usage.
func cpuQuery(quantile float64, namespace, podRegex, window string) string {
	return fmt.Sprintf(
		`quantile_over_time(%s, rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"%s",container!="",container!="POD"}[%s])[%s:%s]) * 1000`,
		formatQuantile(quantile), namespace, podRegex, resolution, window, resolution,
	)
}

// cpuMaxQuery builds a millicores max-over-time query for CPU usage.
func cpuMaxQuery(namespace, podRegex, window string) string {
	return fmt.Sprintf(
		`max_over_time(rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"%s",container!="",container!="POD"}[%s])[%s:%s]) * 1000`,
		namespace, podRegex, resolution, window, resolution,
	)
}

// memQuery builds a bytes quantile-over-time query for memory working set.
func memQuery(quantile float64, namespace, podRegex, window string) string {
	return fmt.Sprintf(
		`quantile_over_time(%s, container_memory_working_set_bytes{namespace="%s",pod=~"%s",container!="",container!="POD"}[%s])`,
		formatQuantile(quantile), namespace, podRegex, window,
	)
}

// memMaxQuery builds a bytes max-over-time query for memory working set.
func memMaxQuery(namespace, podRegex, window string) string {
	return fmt.Sprintf(
		`max_over_time(container_memory_working_set_bytes{namespace="%s",pod=~"%s",container!="",container!="POD"}[%s])`,
		namespace, podRegex, window,
	)
}

// formatQuantile renders a quantile without a trailing ".0" where possible (0.95, 0.5).
func formatQuantile(q float64) string {
	return strconv.FormatFloat(q, 'g', -1, 64)
}

// podRegex builds an anchored PromQL regex matching pods owned by the named workload,
// e.g. "checkout-api" -> "^checkout-api-.*". The workload name is regex-escaped so names
// containing regex metacharacters are matched literally.
func podRegex(name string) string {
	return "^" + regexp.QuoteMeta(name) + "-.*"
}

// parseWindow converts a window string like "14d", "7d", "24h", "30m", "90s" into a
// time.Duration. It accepts the suffixes d (days), h, m, s. Plain time.ParseDuration does
// not understand "d", so days are handled explicitly.
func parseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty window")
	}
	if strings.HasSuffix(s, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid day window %q: %w", s, err)
		}
		if days <= 0 {
			return 0, fmt.Errorf("window must be positive: %q", s)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("window must be positive: %q", s)
	}
	return d, nil
}
