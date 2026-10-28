package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
)

// MemoryController writes the cgroup v2 hard memory limit.
type MemoryController struct{}

// Apply sets memory.max in bytes. A zero limit means no memory controller was requested.
func (MemoryController) Apply(path string, bytes int64) error {
	if bytes <= 0 {
		return fmt.Errorf("memory limit must be greater than zero")
	}
	if err := os.WriteFile(filepath.Join(path, "memory.max"), []byte(fmt.Sprintf("%d", bytes)), 0); err != nil {
		return fmt.Errorf("write memory.max: %w", err)
	}
	return nil
}
