package gitops

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/kubetidy/kubetidy/internal/model"
)

const (
	mi = 1024 * 1024
	gi = 1024 * mi
)

// rec builds a Recommendation for tests. cpuReq/memReq are the *proposed* request
// amounts (millicores / bytes); savings is the monthly dollar delta.
func rec(kind model.WorkloadKind, ns, name, container string, cpuReq, memReq int64, savings float64) model.Recommendation {
	return model.Recommendation{
		Workload: model.Workload{
			Kind:      kind,
			Namespace: ns,
			Name:      name,
		},
		ContainerName: container,
		Current: model.ResourceSpec{
			Requests: model.ResourceAmounts{CPUMillicores: cpuReq * 4, MemoryBytes: memReq * 4},
		},
		Proposed: model.ResourceSpec{
			Requests: model.ResourceAmounts{CPUMillicores: cpuReq, MemoryBytes: memReq},
		},
		MonthlySavings: savings,
		Confidence:     model.Confidence{Score: 0.9},
		Tier:           model.TierHistorical,
	}
}

// patchShape mirrors the strategic-merge patch emitted by patch.StrategicMergePatch
// so we can unmarshal and assert it targets the container resource requests.
type patchShape struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Name      string `json:"name"`
					Resources struct {
						Requests map[string]string `json:"requests"`
						Limits   map[string]string `json:"limits"`
					} `json:"resources"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func TestBuild_BasicChangeSet(t *testing.T) {
	result := model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "prod", "api", "app", 250, 256*mi, 7420),
			rec(model.KindStatefulSet, "data", "db", "postgres", 500, gi, 1200),
		},
	}

	cs, err := Build(result, Options{})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if cs.Count != 2 {
		t.Errorf("Count = %d, want 2", cs.Count)
	}
	if len(cs.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2", len(cs.Files))
	}
	if got, want := cs.TotalMonthlySaved, 8620.0; got != want {
		t.Errorf("TotalMonthlySaved = %v, want %v", got, want)
	}

	for _, f := range cs.Files {
		// Default patch dir prefix.
		if !strings.HasPrefix(f.Path, "kubetidy-patches"+string(filepath.Separator)) {
			t.Errorf("File.Path %q not under default patch dir", f.Path)
		}
		// Contents must end in a newline.
		if len(f.Contents) == 0 || f.Contents[len(f.Contents)-1] != '\n' {
			t.Errorf("File.Contents for %q does not end in newline", f.Path)
		}
		// Valid JSON targeting spec.template.spec.containers[].resources.requests.
		var ps patchShape
		if err := json.Unmarshal(f.Contents, &ps); err != nil {
			t.Errorf("File.Contents for %q is not valid JSON: %v", f.Path, err)
			continue
		}
		conts := ps.Spec.Template.Spec.Containers
		if len(conts) != 1 {
			t.Errorf("patch %q: got %d containers, want 1", f.Path, len(conts))
			continue
		}
		if conts[0].Resources.Requests["cpu"] == "" || conts[0].Resources.Requests["memory"] == "" {
			t.Errorf("patch %q: requests not populated: %+v", f.Path, conts[0].Resources.Requests)
		}
	}
}

func TestBuild_PatchContentMatchesRecommendation(t *testing.T) {
	r := rec(model.KindDeployment, "prod", "api", "app", 250, 256*mi, 100)
	cs, err := Build(model.ScanResult{Recommendations: []model.Recommendation{r}}, Options{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if len(cs.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(cs.Files))
	}
	var ps patchShape
	if err := json.Unmarshal(cs.Files[0].Contents, &ps); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	c := ps.Spec.Template.Spec.Containers[0]
	if c.Name != "app" {
		t.Errorf("container name = %q, want app", c.Name)
	}
	// Compare by quantity value (format-agnostic): 250m CPU, 256Mi memory.
	cpuQ, err := resource.ParseQuantity(c.Resources.Requests["cpu"])
	if err != nil {
		t.Fatalf("parse cpu %q: %v", c.Resources.Requests["cpu"], err)
	}
	if cpuQ.MilliValue() != 250 {
		t.Errorf("cpu = %v millicores, want 250", cpuQ.MilliValue())
	}
	memQ, err := resource.ParseQuantity(c.Resources.Requests["memory"])
	if err != nil {
		t.Fatalf("parse memory %q: %v", c.Resources.Requests["memory"], err)
	}
	if memQ.Value() != 256*mi {
		t.Errorf("mem = %v bytes, want %v", memQ.Value(), 256*mi)
	}
}

func TestPRTitle_LeadsWithDollarsAndCount(t *testing.T) {
	result := model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "prod", "api", "app", 250, 256*mi, 7420),
		},
	}
	cs, err := Build(result, Options{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if !strings.Contains(cs.PRTitle, "save ~$") {
		t.Errorf("PRTitle %q missing 'save ~$'", cs.PRTitle)
	}
	if !strings.Contains(cs.PRTitle, "$7,420") {
		t.Errorf("PRTitle %q missing formatted dollars '$7,420'", cs.PRTitle)
	}
	// Single rec => singular "workload" (not "workloads").
	if !strings.Contains(cs.PRTitle, "1 workload ") {
		t.Errorf("PRTitle %q missing '1 workload'", cs.PRTitle)
	}
	if strings.Contains(cs.PRTitle, "workloads") {
		t.Errorf("PRTitle %q should use singular for one rec", cs.PRTitle)
	}
}

func TestPRTitle_PluralWorkloads(t *testing.T) {
	result := model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "a", "x", "c", 1000, gi, 10),
			rec(model.KindDeployment, "b", "y", "c", 1000, gi, 20),
		},
	}
	cs, _ := Build(result, Options{})
	if !strings.Contains(cs.PRTitle, "workloads") {
		t.Errorf("PRTitle %q should be plural for 2 recs", cs.PRTitle)
	}
}

func TestPRBody_Contents(t *testing.T) {
	result := model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "prod", "api", "app", 250, 256*mi, 7420),
			rec(model.KindStatefulSet, "data", "db", "postgres", 500, gi, 1200),
		},
	}
	cs, err := Build(result, Options{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	body := cs.PRBody

	checks := []string{
		"## kubetidy rightsizing",
		"$7,420", // dollars formatting observable in the summary/rows
		"## How to apply",
		"## How to revert",
		"kubectl patch -f",
		"for f in kubetidy-patches/*.json",
		"| Workload | Container | CPU | Memory | Savings | Confidence |",
		"Deployment/prod/api", // workload Ref()
		"StatefulSet/data/db",
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("PRBody missing %q\n---\n%s", want, body)
		}
	}

	// Total savings is the sum: 7420 + 1200 = 8620 => $8,620.
	if !strings.Contains(body, "$8,620") {
		t.Errorf("PRBody missing total savings '$8,620'\n%s", body)
	}

	// A table data row per rec: count the row lines that reference a workload ref.
	rows := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "| `") && strings.Contains(line, "/") {
			rows++
		}
	}
	if rows != 2 {
		t.Errorf("got %d table data rows, want 2", rows)
	}
}

func TestPRBody_NoTierCaveatForHistory(t *testing.T) {
	result := model.ScanResult{
		Tier: model.TierHistorical,
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "prod", "api", "app", 250, 256*mi, 100),
		},
	}
	cs, _ := Build(result, Options{})
	if strings.Contains(cs.PRBody, "Tier 0") {
		t.Errorf("PRBody should not contain Tier-0 caveat for non-snapshot tier\n%s", cs.PRBody)
	}
}

func TestPRBody_TierSnapshotCaveat(t *testing.T) {
	result := model.ScanResult{
		Tier: model.TierSnapshot,
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "prod", "api", "app", 250, 256*mi, 100),
		},
	}
	cs, _ := Build(result, Options{})
	if !strings.Contains(cs.PRBody, "Tier 0") {
		t.Errorf("PRBody should contain Tier-0 caveat for snapshot tier\n%s", cs.PRBody)
	}
	if !strings.Contains(cs.PRBody, "⚠️") {
		t.Errorf("PRBody should contain warning emoji for snapshot tier\n%s", cs.PRBody)
	}
}

func TestBuild_IncludeGrowFalseExcludesNegative(t *testing.T) {
	result := model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "prod", "api", "app", 250, 256*mi, 100),
			rec(model.KindDeployment, "prod", "small", "app", 500, 512*mi, -50), // grow
		},
	}
	cs, err := Build(result, Options{IncludeGrow: false})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if cs.Count != 1 {
		t.Errorf("Count = %d, want 1 (negative excluded)", cs.Count)
	}
	if strings.Contains(cs.PRBody, "(grow)") {
		t.Errorf("PRBody should not contain '(grow)' when IncludeGrow=false\n%s", cs.PRBody)
	}
	if strings.Contains(cs.PRBody, "prod/small") {
		t.Errorf("PRBody should not reference the excluded grow workload")
	}
}

func TestBuild_IncludeGrowTrueIncludesNegative(t *testing.T) {
	result := model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "prod", "api", "app", 250, 256*mi, 100),
			rec(model.KindDeployment, "prod", "small", "app", 500, 512*mi, -50), // grow
		},
	}
	cs, err := Build(result, Options{IncludeGrow: true})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if cs.Count != 2 {
		t.Errorf("Count = %d, want 2 (grow included)", cs.Count)
	}
	if !strings.Contains(cs.PRBody, "(grow)") {
		t.Errorf("PRBody should render negative savings as '(grow)'\n%s", cs.PRBody)
	}
	// Negative savings must not subtract from the headline total.
	if cs.TotalMonthlySaved != 100 {
		t.Errorf("TotalMonthlySaved = %v, want 100 (grow not subtracted)", cs.TotalMonthlySaved)
	}
}

func TestBuild_TopNLimitsAndRanksBySavingsDesc(t *testing.T) {
	result := model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "ns", "low", "c", 1000, gi, 100),
			rec(model.KindDeployment, "ns", "high", "c", 1000, gi, 900),
			rec(model.KindDeployment, "ns", "mid", "c", 1000, gi, 500),
		},
	}
	cs, err := Build(result, Options{TopN: 2})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if cs.Count != 2 {
		t.Fatalf("Count = %d, want 2", cs.Count)
	}
	// Top 2 by savings: high (900) then mid (500).
	wantOrder := []string{"high", "mid"}
	for i, name := range wantOrder {
		if !strings.Contains(cs.Files[i].Path, name) {
			t.Errorf("Files[%d].Path = %q, want to contain %q", i, cs.Files[i].Path, name)
		}
	}
	// "low" must be excluded by TopN.
	for _, f := range cs.Files {
		if strings.Contains(f.Path, "low") {
			t.Errorf("TopN=2 should exclude 'low', got file %q", f.Path)
		}
	}
	// Total reflects only the included recs.
	if cs.TotalMonthlySaved != 1400 {
		t.Errorf("TotalMonthlySaved = %v, want 1400", cs.TotalMonthlySaved)
	}
}

func TestPatchFileName_SanitizedDeterministic(t *testing.T) {
	tests := []struct {
		name string
		r    model.Recommendation
		want string
	}{
		{
			// Container == workload name: container suffix is omitted.
			name: "lowercased kind/ns/name, container same as name omitted",
			r:    rec(model.KindDeployment, "Prod", "API", "API", 1000, gi, 1),
			want: "deployment-prod-api.json",
		},
		{
			// Distinct container is appended with a dash and sanitized.
			name: "container special chars replaced with dash",
			r:    rec(model.KindStatefulSet, "data", "db", "side car!", 1000, gi, 1),
			want: "statefulset-data-db-side-car-.json",
		},
		{
			// Distinct, already-safe container name is appended and lowercased.
			name: "daemonset with distinct container",
			r:    rec(model.KindDaemonSet, "kube-system", "agent", "Sidecar", 1000, gi, 1),
			want: "daemonset-kube-system-agent-sidecar.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, err := Build(model.ScanResult{Recommendations: []model.Recommendation{tt.r}}, Options{})
			if err != nil {
				t.Fatalf("Build error: %v", err)
			}
			if len(cs.Files) != 1 {
				t.Fatalf("len(Files) = %d, want 1", len(cs.Files))
			}
			gotName := filepath.Base(cs.Files[0].Path)
			if gotName != tt.want {
				t.Errorf("file name = %q, want %q", gotName, tt.want)
			}
		})
	}

	// Determinism: same input twice => identical paths.
	r := rec(model.KindDeployment, "prod", "api", "app", 1000, gi, 1)
	a, _ := Build(model.ScanResult{Recommendations: []model.Recommendation{r}}, Options{})
	b, _ := Build(model.ScanResult{Recommendations: []model.Recommendation{r}}, Options{})
	if a.Files[0].Path != b.Files[0].Path {
		t.Errorf("non-deterministic paths: %q vs %q", a.Files[0].Path, b.Files[0].Path)
	}
}

func TestBuild_CustomPatchDirHonored(t *testing.T) {
	result := model.ScanResult{
		Recommendations: []model.Recommendation{
			rec(model.KindDeployment, "prod", "api", "app", 250, 256*mi, 100),
		},
	}
	cs, err := Build(result, Options{PatchDir: "ops/patches"})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	wantPrefix := "ops/patches" + string(filepath.Separator)
	if !strings.HasPrefix(cs.Files[0].Path, wantPrefix) {
		t.Errorf("File.Path %q not under custom patch dir", cs.Files[0].Path)
	}
	if !strings.Contains(cs.PRBody, "ops/patches/") {
		t.Errorf("PRBody apply instructions should reference custom patch dir\n%s", cs.PRBody)
	}
	if strings.Contains(cs.PRBody, "kubetidy-patches") {
		t.Errorf("PRBody should not reference default patch dir when custom is set\n%s", cs.PRBody)
	}
}

func TestBuild_EmptyRecommendations(t *testing.T) {
	cs, err := Build(model.ScanResult{}, Options{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if cs.Count != 0 {
		t.Errorf("Count = %d, want 0", cs.Count)
	}
	if len(cs.Files) != 0 {
		t.Errorf("len(Files) = %d, want 0", len(cs.Files))
	}
	if cs.TotalMonthlySaved != 0 {
		t.Errorf("TotalMonthlySaved = %v, want 0", cs.TotalMonthlySaved)
	}
	// Title still well-formed: $0 and plural "workloads" for count 0.
	if !strings.Contains(cs.PRTitle, "save ~$0") {
		t.Errorf("PRTitle %q should contain 'save ~$0'", cs.PRTitle)
	}
	if !strings.Contains(cs.PRTitle, "0 workloads") {
		t.Errorf("PRTitle %q should contain '0 workloads'", cs.PRTitle)
	}
	// Body still has the heading.
	if !strings.Contains(cs.PRBody, "## kubetidy rightsizing") {
		t.Errorf("PRBody missing heading for empty result\n%s", cs.PRBody)
	}
}

func TestDollars_ThousandsFormatting(t *testing.T) {
	// dollars() is unexported; observe it via the public PRTitle/PRBody.
	tests := []struct {
		savings float64
		want    string
	}{
		{7420, "$7,420"},
		{999, "$999"},
		{1000000, "$1,000,000"},
		{1234567, "$1,234,567"},
	}
	for _, tt := range tests {
		result := model.ScanResult{
			Recommendations: []model.Recommendation{
				rec(model.KindDeployment, "ns", "w", "c", 1000, gi, tt.savings),
			},
		}
		cs, err := Build(result, Options{})
		if err != nil {
			t.Fatalf("Build error: %v", err)
		}
		if !strings.Contains(cs.PRTitle, tt.want) {
			t.Errorf("savings %v: PRTitle %q missing %q", tt.savings, cs.PRTitle, tt.want)
		}
	}
}
