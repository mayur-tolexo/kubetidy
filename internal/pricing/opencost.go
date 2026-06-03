package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	"github.com/kubetidy/kubetidy/internal/model"
)

// hoursPerMonth is the conventional cloud-billing month length used to convert OpenCost's
// per-hour cost/resource figures into the per-month unit prices kubetidy reports.
const hoursPerMonth = 730.0

// bytesPerGiB converts OpenCost's byte-hours into the GiB-month basis kubetidy uses.
const bytesPerGiB = 1 << 30

// defaultOpenCostWindow is the lookback used when none is supplied. Cost *rates* are stable,
// so a short window keeps the single allocation query cheap and fresh.
const defaultOpenCostWindow = "7d"

// opencostProvider is the Tier-2 PriceProvider: it derives precise per-core-month and
// per-GiB-month rates from OpenCost's allocation API (real allocated cost — spot/reserved/
// committed-use discounts included), replacing the derived node pricing of configProvider.
//
// It queries OpenCost once at construction and serves pure map lookups afterwards, so
// ResourcePrice does no I/O and is safe for concurrent scans.
type opencostProvider struct {
	byWorkload map[string]model.ResourcePrice // "namespace/controller" -> price
	blended    model.ResourcePrice            // cluster-wide blended rate, the fallback
}

// ocAllocation is the subset of an OpenCost allocation entry kubetidy needs to derive rates.
type ocAllocation struct {
	Name       string `json:"name"`
	Properties struct {
		Namespace      string `json:"namespace"`
		Controller     string `json:"controller"`
		ControllerKind string `json:"controllerKind"`
	} `json:"properties"`
	CPUCoreHours float64 `json:"cpuCoreHours"`
	CPUCost      float64 `json:"cpuCost"`
	RAMByteHours float64 `json:"ramByteHours"`
	RAMCost      float64 `json:"ramCost"`
}

// ocResponse is the OpenCost allocation API envelope. With accumulate=true, data holds a
// single map of allocation-name -> allocation; we tolerate more than one element anyway.
type ocResponse struct {
	Code int                       `json:"code"`
	Data []map[string]ocAllocation `json:"data"`
}

// NewOpenCostProvider builds a Tier-2 provider by querying OpenCost's allocation API at
// baseURL (e.g. "http://opencost.opencost.svc:9003") over window (e.g. "7d"). It fetches the
// allocation summary once and computes per-workload + blended unit prices, so callers can fall
// back to configProvider when OpenCost is unreachable or returns no cost data (returns error).
func NewOpenCostProvider(ctx context.Context, baseURL, window string) (Provider, error) {
	return newOpenCostProviderWithClient(ctx, &http.Client{Timeout: 10 * time.Second}, baseURL, window)
}

// NewOpenCostProviderViaAPIProxy builds a Tier-2 provider that reaches an in-cluster OpenCost
// Service through the Kubernetes API server proxy — so `scan`, running on the user's machine,
// can talk to a Service whose DNS only resolves in-cluster, with no port-forward. It reuses the
// kubeconfig's API server address and credentials.
func NewOpenCostProviderViaAPIProxy(ctx context.Context, cfg *rest.Config, ep OpenCostEndpoint, window string) (Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("opencost: nil rest config")
	}
	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("opencost: building API server transport: %w", err)
	}
	base := strings.TrimRight(cfg.Host, "/") +
		fmt.Sprintf("/api/v1/namespaces/%s/services/%s:%d/proxy", ep.Namespace, ep.Service, ep.Port)
	httpc := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	return newOpenCostProviderWithClient(ctx, httpc, base, window)
}

// newOpenCostProviderWithClient is the testable core: it accepts the HTTP client so tests can
// point it at an httptest.Server.
func newOpenCostProviderWithClient(ctx context.Context, httpc *http.Client, baseURL, window string) (Provider, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("opencost: empty base URL")
	}
	if window == "" {
		window = defaultOpenCostWindow
	}

	endpoint, err := allocationURL(baseURL, window)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("opencost: building request: %w", err)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencost: querying allocation API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("opencost: allocation API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var parsed ocResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("opencost: decoding allocation response: %w", err)
	}

	p, err := buildProvider(parsed)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// allocationURL composes the allocation API URL with the query parameters kubetidy needs:
// the window, aggregation by namespace+controller (so each workload maps cleanly), and
// accumulation into a single bucket (we want rates over the window, not a time series).
func allocationURL(baseURL, window string) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/allocation")
	if err != nil {
		return "", fmt.Errorf("opencost: invalid base URL %q: %w", baseURL, err)
	}
	q := u.Query()
	q.Set("window", window)
	q.Set("aggregate", "namespace,controller")
	q.Set("accumulate", "true")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// buildProvider turns a decoded allocation response into per-workload and blended unit prices.
// It skips OpenCost's synthetic __idle__/__unallocated__ buckets so derived rates reflect real
// workloads. It errors when no priceable cost data is present, so the caller can fall back.
func buildProvider(parsed ocResponse) (*opencostProvider, error) {
	byWorkload := make(map[string]model.ResourcePrice)
	var totCPUCost, totCPUCoreHours, totRAMCost, totRAMByteHours float64

	for _, bucket := range parsed.Data {
		for key, a := range bucket {
			if isSyntheticAllocation(key, a) {
				continue
			}
			totCPUCost += a.CPUCost
			totCPUCoreHours += a.CPUCoreHours
			totRAMCost += a.RAMCost
			totRAMByteHours += a.RAMByteHours

			cpu := unitRate(a.CPUCost, a.CPUCoreHours) * hoursPerMonth
			mem := unitRate(a.RAMCost, a.RAMByteHours) * bytesPerGiB * hoursPerMonth
			ns := a.Properties.Namespace
			ctrl := a.Properties.Controller
			if ns != "" && ctrl != "" && (cpu > 0 || mem > 0) {
				byWorkload[ns+"/"+ctrl] = model.ResourcePrice{
					CPUCoreMonth: cpu,
					MemGiBMonth:  mem,
					Source:       "OpenCost",
				}
			}
		}
	}

	blended := model.ResourcePrice{
		CPUCoreMonth: unitRate(totCPUCost, totCPUCoreHours) * hoursPerMonth,
		MemGiBMonth:  unitRate(totRAMCost, totRAMByteHours) * bytesPerGiB * hoursPerMonth,
		Source:       "OpenCost",
	}
	if blended.CPUCoreMonth <= 0 && blended.MemGiBMonth <= 0 && len(byWorkload) == 0 {
		return nil, fmt.Errorf("opencost: no cost data returned (is OpenCost collecting yet?)")
	}
	return &opencostProvider{byWorkload: byWorkload, blended: blended}, nil
}

// isSyntheticAllocation reports whether an allocation is one of OpenCost's pseudo-buckets
// (__idle__, __unallocated__, ...) rather than a real workload.
func isSyntheticAllocation(key string, a ocAllocation) bool {
	if strings.HasPrefix(key, "__") {
		return true
	}
	return a.Properties.Controller == "" && strings.HasPrefix(a.Name, "__")
}

// unitRate returns cost/quantity, or 0 when quantity is non-positive (avoids divide-by-zero
// and NaN for allocations with no recorded usage).
func unitRate(cost, quantity float64) float64 {
	if quantity <= 0 {
		return 0
	}
	return cost / quantity
}

func (p *opencostProvider) Name() string { return "OpenCost" }

// ResourcePrice returns the workload's OpenCost-derived rate, falling back to the cluster
// blended rate for workloads OpenCost has no per-controller figure for (e.g. brand new). It
// never errors: the provider was already validated to hold cost data at construction.
func (p *opencostProvider) ResourcePrice(ctx context.Context, w model.Workload) (model.ResourcePrice, error) {
	_ = ctx
	if pr, ok := p.byWorkload[w.Namespace+"/"+w.Name]; ok {
		return pr, nil
	}
	return p.blended, nil
}
