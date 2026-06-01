package installer

import (
	"strings"
	"testing"
)

// TestDefaultOperatorImageIsPublished guards that the embedded operator manifest and the
// default-image constant agree, and that the default is the published Docker Hub image (not a
// local-only dev tag that would ImagePullBackOff on a real cluster).
func TestDefaultOperatorImageIsPublished(t *testing.T) {
	if !strings.Contains(defaultOperatorImage, "mayurdas1991/kubetidy-operator") {
		t.Errorf("defaultOperatorImage = %q, want the published Docker Hub repo", defaultOperatorImage)
	}
	if !strings.Contains(string(OperatorManifest()), defaultOperatorImage) {
		t.Errorf("operator manifest does not reference defaultOperatorImage %q", defaultOperatorImage)
	}
}

// TestImageOverrideSubstitution verifies the string substitution Install performs when
// Options.Image is set: the embedded default image is replaced by the override everywhere it
// appears.
func TestImageOverrideSubstitution(t *testing.T) {
	const override = "ghcr.io/acme/kubetidy-operator:v1.2.3"
	swapped := strings.ReplaceAll(string(OperatorManifest()), defaultOperatorImage, override)

	if strings.Contains(swapped, defaultOperatorImage) {
		t.Error("default image still present after substitution")
	}
	if !strings.Contains(swapped, override) {
		t.Errorf("override image %q not present after substitution", override)
	}
}

func TestOptionsLogNilDoesNotPanic(t *testing.T) {
	// Zero Options has a nil Log; log() must be a safe no-op.
	Options{}.log("discarded")
	// And a non-nil Log must receive the message.
	var got string
	Options{Log: func(m string) { got = m }}.log("hello")
	if got != "hello" {
		t.Errorf("log delivered %q, want hello", got)
	}
}
