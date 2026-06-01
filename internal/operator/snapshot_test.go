package operator

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/kubetidy/kubetidy/internal/histogram"
)

func TestEncodeDecodeSnapshot_RoundTrip(t *testing.T) {
	h := histogram.New(histogram.DefaultCPUConfig())
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	for i, v := range []float64{10, 20, 30, 40, 250} {
		h.Observe(v, now.Add(time.Duration(i)*time.Minute))
	}
	orig := h.ToSnapshot()

	encoded := encodeSnapshot(orig)
	if encoded == "" {
		t.Fatal("encodeSnapshot returned empty string for a valid snapshot")
	}

	got, ok := decodeSnapshot(encoded)
	if !ok {
		t.Fatal("decodeSnapshot returned ok=false for valid input")
	}
	if got.MaxSeen != orig.MaxSeen {
		t.Errorf("MaxSeen = %v, want %v", got.MaxSeen, orig.MaxSeen)
	}
	if got.NumBuckets != orig.NumBuckets {
		t.Errorf("NumBuckets = %d, want %d", got.NumBuckets, orig.NumBuckets)
	}
	if got.FirstBucketUpper != orig.FirstBucketUpper {
		t.Errorf("FirstBucketUpper = %v, want %v", got.FirstBucketUpper, orig.FirstBucketUpper)
	}
	if got.Ratio != orig.Ratio {
		t.Errorf("Ratio = %v, want %v", got.Ratio, orig.Ratio)
	}
	if got.RefTimeUnixNano != orig.RefTimeUnixNano {
		t.Errorf("RefTimeUnixNano = %d, want %d", got.RefTimeUnixNano, orig.RefTimeUnixNano)
	}
	if len(got.Weights) != len(orig.Weights) {
		t.Fatalf("weights len = %d, want %d", len(got.Weights), len(orig.Weights))
	}
	for i := range got.Weights {
		if got.Weights[i] != orig.Weights[i] {
			t.Errorf("weight[%d] = %v, want %v", i, got.Weights[i], orig.Weights[i])
		}
	}
}

func TestEncodeDecodeSnapshot_RebuildsUsableHistogram(t *testing.T) {
	h := histogram.New(histogram.DefaultCPUConfig())
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	h.Observe(500, now)

	encoded := encodeSnapshot(h.ToSnapshot())
	snap, ok := decodeSnapshot(encoded)
	if !ok {
		t.Fatal("decodeSnapshot ok=false")
	}
	rebuilt := histogram.FromSnapshot(snap, histogram.DefaultCPUConfig())
	if rebuilt.Max() != 500 {
		t.Errorf("rebuilt Max = %v, want 500", rebuilt.Max())
	}
	if rebuilt.IsEmpty() {
		t.Error("rebuilt histogram should not be empty")
	}
}

func TestDecodeSnapshot_EmptyString(t *testing.T) {
	if _, ok := decodeSnapshot(""); ok {
		t.Error(`decodeSnapshot("") should return ok=false`)
	}
}

func TestDecodeSnapshot_InvalidBase64(t *testing.T) {
	if _, ok := decodeSnapshot("not valid base64 @@@"); ok {
		t.Error("decodeSnapshot of invalid base64 should return ok=false")
	}
}

func TestDecodeSnapshot_InvalidJSON(t *testing.T) {
	// Valid base64, but the decoded bytes are not valid JSON for histogram.Snapshot.
	bad := base64.StdEncoding.EncodeToString([]byte("{not json"))
	if _, ok := decodeSnapshot(bad); ok {
		t.Error("decodeSnapshot of invalid JSON should return ok=false")
	}
}

func TestEncodeDecodeSnapshot_Empty(t *testing.T) {
	encoded := encodeSnapshot(histogram.Snapshot{})
	if encoded == "" {
		t.Fatal("encoding a zero snapshot should still produce a string")
	}
	got, ok := decodeSnapshot(encoded)
	if !ok {
		t.Fatal("decoding an encoded zero snapshot should be ok")
	}
	if got.MaxSeen != 0 || len(got.Weights) != 0 {
		t.Errorf("round-tripped zero snapshot has data: %+v", got)
	}
}
