package patch

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/kubetidy/kubetidy/internal/model"
)

func rec(kind model.WorkloadKind, name, ns, container string, req, lim model.ResourceAmounts) model.Recommendation {
	return model.Recommendation{
		Workload:      model.Workload{Kind: kind, Name: name, Namespace: ns},
		ContainerName: container,
		Proposed:      model.ResourceSpec{Requests: req, Limits: lim},
	}
}

// parsePatch unmarshals the strategic-merge patch back into a navigable structure.
func parsePatch(t *testing.T, b []byte) containerDoc {
	t.Helper()
	var doc patchDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("patch is not valid JSON: %v\n%s", err, b)
	}
	if len(doc.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("want exactly 1 container in patch, got %d", len(doc.Spec.Template.Spec.Containers))
	}
	return doc.Spec.Template.Spec.Containers[0]
}

func TestStrategicMergePatchRequestsRoundTrip(t *testing.T) {
	r := rec(model.KindDeployment, "checkout-api", "shop", "checkout-api",
		model.ResourceAmounts{CPUMillicores: 320, MemoryBytes: 1181116006},
		model.ResourceAmounts{})

	b, err := StrategicMergePatch(r)
	if err != nil {
		t.Fatal(err)
	}
	c := parsePatch(t, b)

	if c.Name != "checkout-api" {
		t.Errorf("container name = %q, want checkout-api", c.Name)
	}
	// CPU should be the canonical milli string.
	if got := c.Resources.Requests["cpu"]; got != "320m" {
		t.Errorf("cpu request = %q, want 320m", got)
	}
	// Memory must round-trip to the exact original byte count.
	memStr := c.Resources.Requests["memory"]
	q := resource.MustParse(memStr)
	if q.Value() != 1181116006 {
		t.Errorf("memory request %q parses to %d bytes, want 1181116006", memStr, q.Value())
	}
	// No limits were proposed, so the limits key must be absent.
	if c.Resources.Limits != nil {
		t.Errorf("limits should be omitted, got %+v", c.Resources.Limits)
	}
}

func TestStrategicMergePatchIncludesLimits(t *testing.T) {
	r := rec(model.KindStatefulSet, "db", "data", "db",
		model.ResourceAmounts{CPUMillicores: 500, MemoryBytes: 2 * 1024 * 1024 * 1024},
		model.ResourceAmounts{MemoryBytes: 2 * 1024 * 1024 * 1024})
	b, err := StrategicMergePatch(r)
	if err != nil {
		t.Fatal(err)
	}
	c := parsePatch(t, b)
	if c.Resources.Limits == nil || c.Resources.Limits["memory"] == "" {
		t.Errorf("expected memory limit in patch, got %+v", c.Resources.Limits)
	}
	if _, ok := c.Resources.Limits["cpu"]; ok {
		t.Errorf("did not expect a cpu limit, got %+v", c.Resources.Limits)
	}
}

func TestStrategicMergePatchOmitsZeroRequests(t *testing.T) {
	// CPU-only proposal: memory request must be omitted, not zero.
	r := rec(model.KindDeployment, "web", "shop", "web",
		model.ResourceAmounts{CPUMillicores: 100},
		model.ResourceAmounts{})
	b, err := StrategicMergePatch(r)
	if err != nil {
		t.Fatal(err)
	}
	c := parsePatch(t, b)
	if _, ok := c.Resources.Requests["memory"]; ok {
		t.Errorf("memory request should be omitted, got %+v", c.Resources.Requests)
	}
	if c.Resources.Requests["cpu"] != "100m" {
		t.Errorf("cpu request = %q, want 100m", c.Resources.Requests["cpu"])
	}
}

func TestStrategicMergePatchRequiresContainer(t *testing.T) {
	if _, err := StrategicMergePatch(model.Recommendation{}); err == nil {
		t.Error("expected error for recommendation with no container name")
	}
}

func TestKubectlCommand(t *testing.T) {
	r := rec(model.KindDeployment, "checkout-api", "shop", "checkout-api",
		model.ResourceAmounts{CPUMillicores: 320}, model.ResourceAmounts{})
	cmd, err := KubectlCommand(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kubectl patch deployment checkout-api",
		"-n shop",
		"--type=strategic",
		`"cpu":"320m"`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q in:\n%s", want, cmd)
		}
	}
}

func TestKubectlCommandNoNamespace(t *testing.T) {
	r := rec(model.KindDaemonSet, "node-agent", "", "agent",
		model.ResourceAmounts{CPUMillicores: 50}, model.ResourceAmounts{})
	cmd, err := KubectlCommand(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cmd, " -n ") {
		t.Errorf("expected no namespace flag, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "kubectl patch daemonset node-agent") {
		t.Errorf("wrong kind/name in:\n%s", cmd)
	}
}

func TestKindArg(t *testing.T) {
	cases := map[model.WorkloadKind]string{
		model.KindDeployment:  "deployment",
		model.KindStatefulSet: "statefulset",
		model.KindDaemonSet:   "daemonset",
	}
	for k, want := range cases {
		if got := kindArg(k); got != want {
			t.Errorf("kindArg(%v) = %q, want %q", k, got, want)
		}
	}
}
