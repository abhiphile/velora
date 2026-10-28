// Package cgroup manages Linux cgroups v2 resource limits.
package cgroup

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// Resources contains optional limits for one runtime process tree.
type Resources struct {
	MemoryBytes int64
	CPUCores    float64
	PIDsMax     int64
}

// Validate rejects resource limits that the kernel interface cannot represent safely.
func (resources Resources) Validate() error {
	if resources.MemoryBytes < 0 || resources.PIDsMax < 0 || resources.CPUCores < 0 || math.IsNaN(resources.CPUCores) || math.IsInf(resources.CPUCores, 0) {
		return fmt.Errorf("resource limits must be finite and non-negative")
	}
	return nil
}

// Empty reports whether the caller requested any limits.
func (resources Resources) Empty() bool {
	return resources.MemoryBytes == 0 && resources.CPUCores == 0 && resources.PIDsMax == 0
}

// Cgroup is an opaque handle owned by a cgroups v2 manager.
type Cgroup struct {
	id   string
	path string
}

// ID returns the generated cgroup name for logs and diagnostics.
func (group *Cgroup) ID() string { return group.id }

// Manager owns cgroup creation, process membership, and cleanup.
type Manager interface {
	Create(context.Context, Resources) (*Cgroup, error)
	AddProcess(context.Context, *Cgroup, int) error
	Remove(context.Context, *Cgroup) error
}

// NewManager creates a platform-specific cgroup manager rooted at root.
func NewManager(root string, logger *slog.Logger) (Manager, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("cgroup root must be an absolute path: %q", root)
	}
	if logger == nil {
		return nil, fmt.Errorf("cgroup manager requires a logger")
	}
	return newManager(root, logger)
}

// ParseMemoryLimit parses byte quantities such as 512M, 1G, or 4096.
func ParseMemoryLimit(value string) (int64, error) {
	value = strings.TrimSpace(value)
	index := 0
	for index < len(value) && value[index] >= '0' && value[index] <= '9' {
		index++
	}
	if index == 0 {
		return 0, fmt.Errorf("invalid memory limit %q", value)
	}
	number, err := strconv.ParseUint(value[:index], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse memory limit %q: %w", value, err)
	}
	unit := strings.ToUpper(value[index:])
	var multiplier uint64
	switch unit {
	case "", "B":
		multiplier = 1
	case "K", "KB", "KIB":
		multiplier = 1 << 10
	case "M", "MB", "MIB":
		multiplier = 1 << 20
	case "G", "GB", "GIB":
		multiplier = 1 << 30
	case "T", "TB", "TIB":
		multiplier = 1 << 40
	default:
		return 0, fmt.Errorf("unsupported memory unit %q", unit)
	}
	if number == 0 || number > uint64(math.MaxInt64)/multiplier {
		return 0, fmt.Errorf("memory limit %q is outside the supported range", value)
	}
	return int64(number * multiplier), nil
}
