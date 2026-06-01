package usageprofile

import "testing"

func TestObjectName(t *testing.T) {
	tests := []struct {
		kind, name string
		want       string
	}{
		// The bug from production: an uppercase Kind must be lowercased to a valid RFC 1123 name.
		{"Deployment", "agent-platform-backend", "deployment-agent-platform-backend"},
		{"StatefulSet", "cache", "statefulset-cache"},
		{"DaemonSet", "node-agent", "daemonset-node-agent"},
		// Characters outside [a-z0-9.-] become '-'.
		{"Deployment", "my_app", "deployment-my-app"},
		{"Deployment", "App.v2", "deployment-app.v2"},
		// Trimming leading/trailing separators so the name starts and ends alphanumeric.
		{"Deployment", "-weird-", "deployment--weird"},
		// Fully-invalid input still yields a valid name.
		{"", "", "x"},
		{"!!!", "", "x"},
	}
	for _, tt := range tests {
		if got := ObjectName(tt.kind, tt.name); got != tt.want {
			t.Errorf("ObjectName(%q, %q) = %q, want %q", tt.kind, tt.name, got, tt.want)
		}
	}
}

// TestObjectNameIsRFC1123 sanity-checks that names produced for realistic workloads are valid
// RFC 1123 subdomains (lowercase alphanumerics, '-', '.', start/end alphanumeric).
func TestObjectNameIsRFC1123(t *testing.T) {
	names := []string{
		ObjectName("Deployment", "agent-platform-backend"),
		ObjectName("StatefulSet", "Signoz-ClickHouse-0"),
		ObjectName("DaemonSet", "otel_collector"),
	}
	for _, n := range names {
		if n == "" {
			t.Fatal("empty name")
		}
		if !isAlphanum(rune(n[0])) || !isAlphanum(rune(n[len(n)-1])) {
			t.Errorf("%q must start and end with an alphanumeric", n)
		}
		for _, r := range n {
			ok := isAlphanum(r) || r == '-' || r == '.'
			if !ok {
				t.Errorf("%q contains invalid rune %q", n, r)
			}
		}
	}
}

func isAlphanum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
