package operator

import (
	"encoding/base64"
	"encoding/json"

	"github.com/kubetidy/kubetidy/internal/histogram"
)

// encodeSnapshot serialises a histogram snapshot to a compact base64 string for storage in a
// UsageProfile's MetricHistory.Histogram field. On the (practically impossible) marshal error
// it returns "" so the caller still persists the summary percentiles.
func encodeSnapshot(s histogram.Snapshot) string {
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// decodeSnapshot is the inverse of encodeSnapshot. An empty or malformed string yields a zero
// Snapshot and ok=false, so callers fall back to a fresh histogram rather than failing.
func decodeSnapshot(encoded string) (histogram.Snapshot, bool) {
	if encoded == "" {
		return histogram.Snapshot{}, false
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return histogram.Snapshot{}, false
	}
	var s histogram.Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return histogram.Snapshot{}, false
	}
	return s, true
}
