// Package version exposes build metadata, set via -ldflags at release time.
package version

// These are overridden at build time:
//
//	-ldflags "-X github.com/kubetidy/kubetidy/internal/version.Version=v1.2.3 ..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a single-line version summary.
func String() string {
	return Version + " (commit " + Commit + ", built " + Date + ")"
}
