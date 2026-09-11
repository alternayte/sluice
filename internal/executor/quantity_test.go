package executor_test

import (
	"testing"

	"github.com/alternayte/sluice/internal/executor"
)

func TestParseQuantities(t *testing.T) {
	cpus := map[string]int64{"500m": 500_000_000, "2": 2_000_000_000, "0.5": 500_000_000, "": 0}
	for in, want := range cpus {
		if got, err := executor.ParseCPU(in); err != nil || got != want {
			t.Errorf("ParseCPU(%q) = %d %v, want %d", in, got, err, want)
		}
	}
	mems := map[string]int64{"512Mi": 512 << 20, "1G": 1_000_000_000, "1048576": 1 << 20, "2Gi": 2 << 30, "": 0}
	for in, want := range mems {
		if got, err := executor.ParseMemory(in); err != nil || got != want {
			t.Errorf("ParseMemory(%q) = %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"abc", "-1", "1Xi"} {
		if _, err := executor.ParseMemory(bad); err == nil {
			t.Errorf("ParseMemory(%q) accepted", bad)
		}
	}
	if _, err := executor.ParseCPU("x"); err == nil {
		t.Error("ParseCPU accepted x")
	}
}
