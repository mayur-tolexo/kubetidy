package histogram

import (
	"testing"
	"time"
)

func TestDefaultHalfLifeIsOneWeek(t *testing.T) {
	if DefaultHalfLife != 7*24*time.Hour {
		t.Errorf("DefaultHalfLife = %s, want 168h (1 week)", DefaultHalfLife)
	}
	if DefaultCPUConfig().HalfLife != DefaultHalfLife {
		t.Error("DefaultCPUConfig should use DefaultHalfLife")
	}
	if DefaultMemoryConfig().HalfLife != DefaultHalfLife {
		t.Error("DefaultMemoryConfig should use DefaultHalfLife")
	}
}

func TestConfigWithHalfLife(t *testing.T) {
	base := DefaultCPUConfig()

	// A positive override is applied; the rest of the config is preserved.
	got := base.WithHalfLife(30 * 24 * time.Hour)
	if got.HalfLife != 30*24*time.Hour {
		t.Errorf("HalfLife = %s, want 720h", got.HalfLife)
	}
	if got.NumBuckets != base.NumBuckets || got.Ratio != base.Ratio || got.FirstBucketUpper != base.FirstBucketUpper {
		t.Error("WithHalfLife should preserve bucket layout")
	}

	// A non-positive override leaves the existing half-life unchanged (flag fallback).
	if base.WithHalfLife(0).HalfLife != base.HalfLife {
		t.Error("WithHalfLife(0) should keep the existing half-life")
	}
	if base.WithHalfLife(-time.Hour).HalfLife != base.HalfLife {
		t.Error("WithHalfLife(negative) should keep the existing half-life")
	}
}
