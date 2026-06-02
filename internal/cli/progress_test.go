package cli

import (
	"strings"
	"testing"
)

func TestScanProgress(t *testing.T) {
	start := scanProgress(0, 102)
	if !strings.Contains(start, "0/102") || !strings.Contains(start, scanPhrases[0]) {
		t.Errorf("start = %q", start)
	}
	end := scanProgress(102, 102)
	if !strings.Contains(end, "102/102") || !strings.Contains(end, scanPhrases[len(scanPhrases)-1]) {
		t.Errorf("end = %q", end)
	}
	// Robust to edge inputs (no panic / out-of-range).
	_ = scanProgress(0, 0)
	_ = scanProgress(5, 1) // done > total clamps
}
