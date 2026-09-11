package executor

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseCPU returns nano CPUs for a CPU quantity such as "500m", "2" or "0.5" (§6.4).
func ParseCPU(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if milli, ok := strings.CutSuffix(s, "m"); ok {
		v, err := strconv.ParseFloat(milli, 64)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("invalid CPU quantity %q", s)
		}
		return int64(v * 1e6), nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid CPU quantity %q", s)
	}
	return int64(v * 1e9), nil
}

var memoryUnits = []struct {
	suffix string
	factor int64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
	{"K", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
}

// ParseMemory returns bytes for a memory quantity such as "512Mi", "1G" or "1048576" (§6.4).
func ParseMemory(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	for _, u := range memoryUnits {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseInt(n, 10, 64)
			if err != nil || v < 0 {
				return 0, fmt.Errorf("invalid memory quantity %q", s)
			}
			return v * u.factor, nil
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid memory quantity %q", s)
	}
	return v, nil
}
