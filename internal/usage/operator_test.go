package usage

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
	"github.com/kubetidy/kubetidy/internal/model"
)

// newOperatorClient builds a fake dynamic client seeded with the given UsageProfile
// objects, wiring up the GVR->ListKind mapping the fake needs for an unstructured
// resource. Each profile is converted via ToUnstructured() and stamped with its GVK
// so the fake stores it under the right GVR and namespace.
func newOperatorClient(t *testing.T, profiles ...usageprofile.UsageProfile) *dynfake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	gvr := usageprofile.GroupVersionResource()
	listKinds := map[schema.GroupVersionResource]string{gvr: "UsageProfileList"}

	objs := make([]runtime.Object, 0, len(profiles))
	for i := range profiles {
		p := profiles[i]
		u := p.ToUnstructured()
		u.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   usageprofile.Group,
			Version: usageprofile.Version,
			Kind:    usageprofile.Kind,
		})
		objs = append(objs, u)
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

// sampleProfile builds a UsageProfile for the given workload kind/name in namespace ns.
// The object name follows the "<Kind>-<name>" convention the provider looks up by.
func sampleProfile(ns string, kind model.WorkloadKind, name string) usageprofile.UsageProfile {
	return usageprofile.UsageProfile{
		Name:      fmt.Sprintf("%s-%s", kind, name),
		Namespace: ns,
		Spec: usageprofile.Spec{
			TargetRef: usageprofile.TargetRef{Kind: string(kind), Name: name},
		},
		Status: usageprofile.Status{
			SampleCount:   4032,
			WindowSeconds: 1209600, // 14d in seconds
			Containers: []usageprofile.ContainerHistory{
				{
					Name:   "app",
					CPU:    usageprofile.MetricHistory{P50: 120, P95: 280, Max: 410},
					Memory: usageprofile.MetricHistory{P50: 100 << 20, P95: 256 << 20, Max: 512 << 20},
				},
				{
					Name:   "sidecar",
					CPU:    usageprofile.MetricHistory{P50: 5, P95: 12, Max: 30},
					Memory: usageprofile.MetricHistory{P50: 16 << 20, P95: 32 << 20, Max: 48 << 20},
				},
			},
		},
	}
}

func TestOperatorProvider_NameAndTier(t *testing.T) {
	p := NewOperatorProvider(newOperatorClient(t))
	if got := p.Name(); got != "kubetidy operator" {
		t.Errorf("Name() = %q, want %q", got, "kubetidy operator")
	}
	if got := p.Tier(); got != model.TierOperator {
		t.Errorf("Tier() = %v, want %v", got, model.TierOperator)
	}
}

func TestOperatorProvider_SeededFixtureIsRetrievable(t *testing.T) {
	// Guard: confirm the fake actually stores the object under the expected GVR/
	// namespace/name, so a later Usage miss means "provider bug", not "bad fixture".
	const ns = "shop"
	prof := sampleProfile(ns, model.KindDeployment, "checkout")
	client := newOperatorClient(t, prof)

	got, err := client.
		Resource(usageprofile.GroupVersionResource()).
		Namespace(ns).
		Get(context.Background(), "Deployment-checkout", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("seeded fixture not retrievable: %v", err)
	}
	if got.GetName() != "Deployment-checkout" {
		t.Fatalf("retrieved name = %q, want %q", got.GetName(), "Deployment-checkout")
	}
}

func TestOperatorProvider_Usage_MapsProfile(t *testing.T) {
	const ns = "shop"
	prof := sampleProfile(ns, model.KindDeployment, "checkout")
	client := newOperatorClient(t, prof)
	p := NewOperatorProvider(client)

	w := model.Workload{
		Kind:      model.KindDeployment,
		Name:      "checkout",
		Namespace: ns,
		Containers: []model.Container{
			{Name: "app"},
			{Name: "sidecar"},
		},
		Selector: map[string]string{"app": "checkout"},
	}

	stats, err := p.Usage(context.Background(), w)
	if err != nil {
		t.Fatalf("Usage() error = %v, want nil", err)
	}
	if len(stats) != 2 {
		t.Fatalf("Usage() returned %d containers, want 2: %#v", len(stats), stats)
	}

	app, ok := stats["app"]
	if !ok {
		t.Fatalf("Usage() missing container %q", "app")
	}
	if app.CPUMillicores.P50 != 120 || app.CPUMillicores.P95 != 280 || app.CPUMillicores.Max != 410 {
		t.Errorf("app CPU = %+v, want P50=120 P95=280 Max=410", app.CPUMillicores)
	}
	if app.MemoryBytes.P50 != float64(100<<20) ||
		app.MemoryBytes.P95 != float64(256<<20) ||
		app.MemoryBytes.Max != float64(512<<20) {
		t.Errorf("app Memory = %+v, want P50=%d P95=%d Max=%d",
			app.MemoryBytes, 100<<20, 256<<20, 512<<20)
	}
	if app.Tier != model.TierOperator {
		t.Errorf("app Tier = %v, want %v", app.Tier, model.TierOperator)
	}
	if app.Samples != 4032 {
		t.Errorf("app Samples = %d, want 4032", app.Samples)
	}
	wantWindow := time.Duration(1209600 * float64(time.Second))
	if app.Window != wantWindow {
		t.Errorf("app Window = %v, want %v", app.Window, wantWindow)
	}

	side, ok := stats["sidecar"]
	if !ok {
		t.Fatalf("Usage() missing container %q", "sidecar")
	}
	if side.CPUMillicores.P95 != 12 || side.MemoryBytes.Max != float64(48<<20) {
		t.Errorf("sidecar mapped wrong: %+v / %+v", side.CPUMillicores, side.MemoryBytes)
	}
	if side.Tier != model.TierOperator || side.Samples != 4032 || side.Window != wantWindow {
		t.Errorf("sidecar metadata wrong: tier=%v samples=%d window=%v",
			side.Tier, side.Samples, side.Window)
	}
}

func TestOperatorProvider_Usage_NoProfileIsGraceful(t *testing.T) {
	// Cluster has one profile, but for a different workload than we query.
	const ns = "shop"
	other := sampleProfile(ns, model.KindDeployment, "checkout")
	p := NewOperatorProvider(newOperatorClient(t, other))

	w := model.Workload{
		Kind:      model.KindDeployment,
		Name:      "not-observed-yet",
		Namespace: ns,
		Containers: []model.Container{
			{Name: "app"},
		},
	}

	stats, err := p.Usage(context.Background(), w)
	if err != nil {
		t.Fatalf("Usage() for unprofiled workload error = %v, want nil", err)
	}
	if stats == nil {
		t.Fatal("Usage() returned nil map, want empty non-nil map")
	}
	if len(stats) != 0 {
		t.Fatalf("Usage() for unprofiled workload = %#v, want empty map", stats)
	}
}

func TestDetectOperator_NilClient(t *testing.T) {
	if DetectOperator(nil) {
		t.Error("DetectOperator(nil) = true, want false")
	}
}

func TestDetectOperator_NoProfiles(t *testing.T) {
	client := newOperatorClient(t)
	if DetectOperator(client) {
		t.Error("DetectOperator(empty) = true, want false")
	}
}

func TestDetectOperator_WithProfiles(t *testing.T) {
	client := newOperatorClient(t,
		sampleProfile("shop", model.KindDeployment, "checkout"),
		sampleProfile("ops", model.KindStatefulSet, "db"),
	)
	if !DetectOperator(client) {
		t.Error("DetectOperator(>=1 profile) = false, want true")
	}
}

// compile-time assertion: the operator provider satisfies the Provider interface.
var _ Provider = (*operatorProvider)(nil)
