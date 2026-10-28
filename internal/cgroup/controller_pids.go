package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
)

// PIDsController writes the cgroup v2 task-count limit.
type PIDsController struct{}

// Apply sets pids.max, which counts kernel tasks including threads.
func (PIDsController) Apply(path string, maximum int64) error {
	if maximum <= 0 {
		return fmt.Errorf("PID limit must be greater than zero")
	}
	if err := os.WriteFile(filepath.Join(path, "pids.max"), []byte(fmt.Sprintf("%d", maximum)), 0); err != nil {
		return fmt.Errorf("write pids.max: %w", err)
	}
	return nil
}
