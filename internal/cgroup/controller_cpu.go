package cgroup

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// CPUController writes a hard CPU bandwidth limit to cpu.max.
type CPUController struct{}

// Apply converts a CPU-core allowance into quota/period microseconds.
func (CPUController) Apply(path string, cores float64) error {
	const periodMicros int64 = 100_000
	if cores <= 0 || math.IsNaN(cores) || math.IsInf(cores, 0) {
		return fmt.Errorf("CPU limit must be finite and greater than zero")
	}
	if cores > float64(math.MaxInt64/periodMicros) {
		return fmt.Errorf("CPU limit is too large to represent in cpu.max")
	}
	quota := int64(math.Round(cores * float64(periodMicros)))
	if quota < 1_000 {
		return fmt.Errorf("CPU limit must allow at least one millisecond per period")
	}
	value := fmt.Sprintf("%d %d", quota, periodMicros)
	if err := os.WriteFile(filepath.Join(path, "cpu.max"), []byte(value), 0); err != nil {
		return fmt.Errorf("write cpu.max: %w", err)
	}
	return nil
}
