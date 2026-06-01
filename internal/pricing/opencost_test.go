package pricing

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/kubetidy/kubetidy/internal/model"
)

// allocationBody is a representative OpenCost /allocation response (accumulate=true → one
// bucket). checkout-api: $0.79 over 24 core-hours → $0.0329/core-hr; $0.32 over 1.7e13
// byte-hours. Plus a synthetic __idle__ bucket that must be ignored.
const allocationBody = `{
  "code": 200,
  "data": [
    {
      "kubetidy-demo/checkout-api": {
        "name": "kubetidy-demo/checkout-api",
        "properties": {"namespace": "kubetidy-demo", "controller": "checkout-api", "controllerKind": "deployment"},
        "cpuCoreHours": 24.0,
        "cpuCost": 0.79,
        "ramByteHours": 17179869184000.0,
        "ramCost": 0.32
      },
      "__idle__": {
        "name": "__idle__",
        "properties": {},
        "cpuCoreHours": 100.0,
        "cpuCost": 99.0,
        "ramByteHours": 1.0,
        "ramCost": 99.0
      }
    }
  ]
}`

func newTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assert the request shape kubetidy sends.
		if r.URL.Path != "/allocation" {
			t.Errorf("path = %q, want /allocation", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("aggregate") != "namespace,controller" {
			t.Errorf("aggregate = %q, want namespace,controller", q.Get("aggregate"))
		}
		if q.Get("accumulate") != "true" {
			t.Errorf("accumulate = %q, want true", q.Get("accumulate"))
		}
		if q.Get("window") == "" {
			t.Error("window query param missing")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func approx(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 0.5 {
		t.Errorf("%s = %.3f, want ~%.3f", label, got, want)
	}
}

func TestOpenCostProvider_DerivesUnitPrices(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, allocationBody)
	p, err := newOpenCostProviderWithClient(context.Background(), srv.Client(), srv.URL, "7d")
	if err != nil {
		t.Fatalf("NewOpenCostProvider: %v", err)
	}
	if p.Name() != "OpenCost" {
		t.Errorf("Name = %q, want OpenCost", p.Name())
	}

	price, err := p.ResourcePrice(context.Background(), model.Workload{Namespace: "kubetidy-demo", Name: "checkout-api"})
	if err != nil {
		t.Fatalf("ResourcePrice: %v", err)
	}
	if price.Source != "OpenCost" {
		t.Errorf("Source = %q, want OpenCost", price.Source)
	}
	// $0.79 / 24 core-hr * 730 hr/mo ≈ $24.03/core-mo.
	approx(t, price.CPUCoreMonth, 0.79/24.0*730.0, "CPUCoreMonth")
	// $0.32 / 1.7179869184e13 byte-hr * (1<<30) bytes/GiB * 730 hr/mo ≈ $14.6/GiB-mo.
	approx(t, price.MemGiBMonth, 0.32/17179869184000.0*float64(1<<30)*730.0, "MemGiBMonth")
}

func TestOpenCostProvider_IgnoresIdleBucket(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, allocationBody)
	p, err := newOpenCostProviderWithClient(context.Background(), srv.Client(), srv.URL, "7d")
	if err != nil {
		t.Fatalf("NewOpenCostProvider: %v", err)
	}
	// The __idle__ bucket has wildly inflated rates; if it leaked into the blended fallback the
	// price for an unknown workload would be absurd.
	price, _ := p.ResourcePrice(context.Background(), model.Workload{Namespace: "other", Name: "unknown"})
	if price.CPUCoreMonth > 100 {
		t.Errorf("blended CPUCoreMonth = %.1f — __idle__ bucket leaked in", price.CPUCoreMonth)
	}
}

func TestOpenCostProvider_UnknownWorkloadGetsBlended(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, allocationBody)
	p, _ := newOpenCostProviderWithClient(context.Background(), srv.Client(), srv.URL, "7d")

	known, _ := p.ResourcePrice(context.Background(), model.Workload{Namespace: "kubetidy-demo", Name: "checkout-api"})
	unknown, _ := p.ResourcePrice(context.Background(), model.Workload{Namespace: "x", Name: "y"})
	// With a single real workload, the blended rate equals that workload's rate.
	approx(t, unknown.CPUCoreMonth, known.CPUCoreMonth, "blended CPUCoreMonth")
	if unknown.Source != "OpenCost" {
		t.Errorf("blended Source = %q, want OpenCost", unknown.Source)
	}
}

func TestOpenCostProvider_ErrorsOnNon200(t *testing.T) {
	srv := newTestServer(t, http.StatusServiceUnavailable, "nope")
	if _, err := newOpenCostProviderWithClient(context.Background(), srv.Client(), srv.URL, "7d"); err == nil {
		t.Fatal("expected an error on non-200 response")
	}
}

func TestOpenCostProvider_ErrorsOnEmptyData(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, `{"code":200,"data":[]}`)
	if _, err := newOpenCostProviderWithClient(context.Background(), srv.Client(), srv.URL, "7d"); err == nil {
		t.Fatal("expected an error when no cost data is returned")
	}
}

func TestOpenCostProvider_ErrorsOnBadJSON(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, `{not json`)
	if _, err := newOpenCostProviderWithClient(context.Background(), srv.Client(), srv.URL, "7d"); err == nil {
		t.Fatal("expected an error decoding bad JSON")
	}
}

func TestOpenCostProvider_EmptyBaseURL(t *testing.T) {
	if _, err := NewOpenCostProvider(context.Background(), "", "7d"); err == nil {
		t.Fatal("expected an error for an empty base URL")
	}
}

func TestOpenCostProvider_DefaultWindowApplied(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, allocationBody)
	// Empty window should fall back to the default rather than send an empty window param.
	if _, err := newOpenCostProviderWithClient(context.Background(), srv.Client(), srv.URL, ""); err != nil {
		t.Fatalf("NewOpenCostProvider with empty window: %v", err)
	}
}

func TestAllocationURL(t *testing.T) {
	got, err := allocationURL("http://opencost.opencost.svc:9003/", "7d")
	if err != nil {
		t.Fatalf("allocationURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Path != "/allocation" {
		t.Errorf("path = %q, want /allocation (trailing slash on base should be trimmed)", u.Path)
	}
	if u.Query().Get("window") != "7d" {
		t.Errorf("window = %q, want 7d", u.Query().Get("window"))
	}
}

func TestUnitRate_ZeroQuantity(t *testing.T) {
	if r := unitRate(5, 0); r != 0 {
		t.Errorf("unitRate(5,0) = %v, want 0 (no divide-by-zero)", r)
	}
}
