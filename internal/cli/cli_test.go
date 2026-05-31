package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/kubetidy/kubetidy/internal/model"
	"github.com/kubetidy/kubetidy/internal/version"
)

// ---- rootUse / NewRootCommand ------------------------------------------------

func TestRootUse(t *testing.T) {
	tests := []struct {
		name      string
		invokedAs string
		want      string
	}{
		{"kubectl plugin", "kubectl-tidy", "kubectl tidy"},
		{"kubectl plugin full path", "/usr/local/bin/kubectl-tidy", "kubectl tidy"},
		{"plain binary full path", "/usr/local/bin/kubetidy", "kubetidy"},
		{"empty", "", "kubetidy"},
		{"dot", ".", "kubetidy"},
		{"custom name passthrough", "mytool", "mytool"},
		{"custom name with path", "/opt/bin/mytool", "mytool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rootUse(tt.invokedAs); got != tt.want {
				t.Fatalf("rootUse(%q) = %q, want %q", tt.invokedAs, got, tt.want)
			}
		})
	}
}

func TestNewRootCommand(t *testing.T) {
	root := NewRootCommand("kubetidy")
	if root == nil {
		t.Fatal("NewRootCommand returned nil")
	}
	if root.Use != "kubetidy" {
		t.Fatalf("root.Use = %q, want %q", root.Use, "kubetidy")
	}
	if root.Version != version.String() {
		t.Fatalf("root.Version = %q, want %q", root.Version, version.String())
	}
	if !root.SilenceUsage {
		t.Error("expected SilenceUsage to be true")
	}
	if !root.SilenceErrors {
		t.Error("expected SilenceErrors to be true")
	}

	want := map[string]bool{"scan": false, "diff": false, "pr": false, "version": false}
	for _, c := range root.Commands() {
		// c.Name() is the first word of Use.
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered on root", name)
		}
	}
}

func TestNewRootCommandKubectlFace(t *testing.T) {
	root := NewRootCommand("/usr/local/bin/kubectl-tidy")
	if root.Use != "kubectl tidy" {
		t.Fatalf("root.Use = %q, want %q", root.Use, "kubectl tidy")
	}
}

// ---- version -----------------------------------------------------------------

func TestVersionString(t *testing.T) {
	s := version.String()
	if strings.TrimSpace(s) == "" {
		t.Fatal("version.String() is empty")
	}
}

func TestVersionCommand(t *testing.T) {
	// version.go writes to os.Stdout directly (not cmd.OutOrStdout), so SetOut
	// cannot capture it. We instead capture os.Stdout via an os.Pipe and assert
	// the command's Run writes version.String().
	cmd := newVersionCommand()
	if cmd.Use != "version" {
		t.Fatalf("version cmd Use = %q, want %q", cmd.Use, "version")
	}
	if cmd.Run == nil {
		t.Fatal("version cmd Run is nil")
	}

	out := captureStdout(t, func() {
		cmd.Run(cmd, nil)
	})
	if !strings.Contains(out, version.String()) {
		t.Fatalf("version output %q does not contain %q", out, version.String())
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. It restores os.Stdout afterwards.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		done <- sb.String()
	}()

	fn()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// ---- isTTY -------------------------------------------------------------------

func TestIsTTYPipeIsFalse(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	if isTTY(w) {
		t.Error("isTTY(pipe writer) = true, want false")
	}
	if isTTY(r) {
		t.Error("isTTY(pipe reader) = true, want false")
	}
}

// ---- spinner (progress.go) ---------------------------------------------------

func TestSpinnerNoTTYIsNoOp(t *testing.T) {
	// In the test environment stderr is not a TTY, so the spinner must be disabled
	// and every method must be a no-op that does not panic.
	sp := newSpinner("connecting…")
	if sp == nil {
		t.Fatal("newSpinner returned nil")
	}
	if sp.enabled {
		t.Skip("stderr is a TTY in this environment; spinner enabled, skipping no-op assertions")
	}

	// None of these should panic or block when disabled.
	sp.start()
	sp.update("working…")
	sp.finish()

	// Calling finish again on a disabled spinner is still a no-op (it never closes
	// channels when disabled), so it must not panic.
	sp.finish()
}

func TestSpinnerFields(t *testing.T) {
	sp := newSpinner("init")
	if sp.status != "init" {
		t.Fatalf("spinner.status = %q, want %q", sp.status, "init")
	}
	if len(sp.frames) == 0 {
		t.Error("expected non-empty frames")
	}
	if sp.stopCh == nil || sp.doneCh == nil {
		t.Error("expected channels to be initialized")
	}
}

// ---- flag wiring -------------------------------------------------------------

func lookupDefault(t *testing.T, cmd *cobra.Command, flag string) string {
	t.Helper()
	f := cmd.Flags().Lookup(flag)
	if f == nil {
		t.Fatalf("flag %q not found on %q", flag, cmd.Name())
	}
	return f.DefValue
}

func TestScanFlags(t *testing.T) {
	cmd := newScanCommand()
	if cmd.Use != "scan" {
		t.Fatalf("scan Use = %q", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Fatal("scan RunE is nil")
	}
	checks := map[string]string{
		"output":         "table",
		"top":            "20",
		"window":         "14d",
		"namespace":      "",
		"prometheus-url": "",
		"context":        "",
		"explain":        "",
		"cpu-cost":       "0",
		"mem-cost":       "0",
	}
	for flag, def := range checks {
		if got := lookupDefault(t, cmd, flag); got != def {
			t.Errorf("scan flag %q default = %q, want %q", flag, got, def)
		}
	}
	// short flags
	if cmd.Flags().ShorthandLookup("n") == nil {
		t.Error("expected -n shorthand for namespace")
	}
	if cmd.Flags().ShorthandLookup("o") == nil {
		t.Error("expected -o shorthand for output")
	}
}

func TestDiffFlags(t *testing.T) {
	cmd := newDiffCommand()
	if cmd.Use != "diff" {
		t.Fatalf("diff Use = %q", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Fatal("diff RunE is nil")
	}
	checks := map[string]string{
		"top":            "20",
		"window":         "14d",
		"namespace":      "",
		"prometheus-url": "",
		"explain":        "",
	}
	for flag, def := range checks {
		if got := lookupDefault(t, cmd, flag); got != def {
			t.Errorf("diff flag %q default = %q, want %q", flag, got, def)
		}
	}
	// diff has no --output flag (only scan does).
	if cmd.Flags().Lookup("output") != nil {
		t.Error("did not expect --output on diff")
	}
}

func TestPRFlags(t *testing.T) {
	cmd := newPRCommand()
	if cmd.Use != "pr" {
		t.Fatalf("pr Use = %q", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Fatal("pr RunE is nil")
	}
	checks := map[string]string{
		"out":            "kubetidy-patches",
		"top":            "0",
		"window":         "14d",
		"include-grow":   "false",
		"body-out":       "",
		"namespace":      "",
		"prometheus-url": "",
	}
	for flag, def := range checks {
		if got := lookupDefault(t, cmd, flag); got != def {
			t.Errorf("pr flag %q default = %q, want %q", flag, got, def)
		}
	}
}

// ---- diff.go pure helpers ----------------------------------------------------

func TestSignedDollars(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "$0"},
		{7, "$7"},
		{-7, "$-7"},
		{7.4, "$7"},
		{7.5, "$8"},
		{-7.5, "$-8"},
		{1234.49, "$1234"},
		{-0.4, "$-0"},
	}
	for _, tt := range tests {
		if got := signedDollars(tt.in); got != tt.want {
			t.Errorf("signedDollars(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---- renderDiff --------------------------------------------------------------

func makeRec(kind model.WorkloadKind, ns, name, container string, savings float64) model.Recommendation {
	rec := model.Recommendation{ContainerName: container, MonthlySavings: savings}
	rec.Workload.Kind = kind
	rec.Workload.Namespace = ns
	rec.Workload.Name = name
	rec.Confidence = model.Confidence{Score: 0.9}
	return rec
}

func TestRenderDiffEmpty(t *testing.T) {
	var sb strings.Builder
	if err := renderDiff(&sb, model.ScanResult{}, "", 0); err != nil {
		t.Fatalf("renderDiff: %v", err)
	}
	if !strings.Contains(sb.String(), "No rightsizing recommendations.") {
		t.Fatalf("expected empty message, got %q", sb.String())
	}
}

func TestRenderDiffSortedAndFormatted(t *testing.T) {
	result := model.ScanResult{Recommendations: []model.Recommendation{
		makeRec(model.KindDeployment, "web", "small", "c1", 5),
		makeRec(model.KindDeployment, "web", "big", "c2", 100),
	}}
	var sb strings.Builder
	if err := renderDiff(&sb, result, "", 0); err != nil {
		t.Fatalf("renderDiff: %v", err)
	}
	out := sb.String()
	// Sorted by savings desc => "big" header appears before "small".
	bi := strings.Index(out, "big")
	si := strings.Index(out, "small")
	if bi < 0 || si < 0 || bi > si {
		t.Fatalf("expected big before small in output:\n%s", out)
	}
	if !strings.Contains(out, "saves $100/mo") {
		t.Errorf("expected savings line, got:\n%s", out)
	}
	if !strings.Contains(out, "conf 90%") {
		t.Errorf("expected confidence line, got:\n%s", out)
	}
	if !strings.Contains(out, "kubectl patch deployment big -n web") {
		t.Errorf("expected kubectl command, got:\n%s", out)
	}
}

func TestRenderDiffTopN(t *testing.T) {
	result := model.ScanResult{Recommendations: []model.Recommendation{
		makeRec(model.KindDeployment, "web", "a", "c", 30),
		makeRec(model.KindDeployment, "web", "b", "c", 20),
		makeRec(model.KindDeployment, "web", "c", "c", 10),
	}}
	var sb strings.Builder
	if err := renderDiff(&sb, result, "", 1); err != nil {
		t.Fatalf("renderDiff: %v", err)
	}
	out := sb.String()
	if strings.Count(out, "kubectl") != 1 {
		t.Fatalf("topN=1 should emit one command, got:\n%s", out)
	}
}

func TestRenderDiffExplainFilter(t *testing.T) {
	result := model.ScanResult{Recommendations: []model.Recommendation{
		makeRec(model.KindDeployment, "web", "frontend", "c", 30),
		makeRec(model.KindDeployment, "web", "backend", "c", 20),
	}}
	var sb strings.Builder
	if err := renderDiff(&sb, result, "frontend", 0); err != nil {
		t.Fatalf("renderDiff: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "frontend") || strings.Contains(out, "backend") {
		t.Fatalf("explain filter failed, got:\n%s", out)
	}
}

// ---- render ------------------------------------------------------------------

func TestRenderJSON(t *testing.T) {
	result := model.ScanResult{
		Context:       "kind-test",
		WorkloadCount: 2,
	}
	out := captureStdout(t, func() {
		if err := render(result, &scanFlags{output: "json"}); err != nil {
			t.Errorf("render json: %v", err)
		}
	})
	if !strings.Contains(out, "kind-test") {
		t.Fatalf("expected JSON to contain context, got:\n%s", out)
	}
}

func TestRenderTable(t *testing.T) {
	result := model.ScanResult{Context: "kind-test", WorkloadCount: 0}
	out := captureStdout(t, func() {
		if err := render(result, &scanFlags{output: "table", topN: 20}); err != nil {
			t.Errorf("render table: %v", err)
		}
	})
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected table output, got empty")
	}
}

func TestRenderDefaultEmptyFormatIsTable(t *testing.T) {
	result := model.ScanResult{Context: "kind-test"}
	out := captureStdout(t, func() {
		if err := render(result, &scanFlags{output: ""}); err != nil {
			t.Errorf("render default: %v", err)
		}
	})
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected table output for empty format, got empty")
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	err := render(model.ScanResult{}, &scanFlags{output: "yaml"})
	if err == nil {
		t.Fatal("expected error for unknown output format")
	}
	if !strings.Contains(err.Error(), "unknown output format") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMatchesWorkload(t *testing.T) {
	rec := model.Recommendation{ContainerName: "nginx"}
	rec.Workload.Name = "frontend"

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"matches container", "ngin", true},
		{"matches container exact", "nginx", true},
		{"matches workload name", "front", true},
		{"matches workload name exact", "frontend", true},
		{"no match", "database", false},
		{"empty query matches (Contains)", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesWorkload(rec, tt.query); got != tt.want {
				t.Errorf("matchesWorkload(query=%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}
