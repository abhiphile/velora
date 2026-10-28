//go:build !linux

package cgroup

import (
	"context"
	"fmt"
	"log/slog"
)

type unsupportedManager struct{}

func newManager(string, *slog.Logger) (Manager, error) { return unsupportedManager{}, nil }
func (unsupportedManager) Create(context.Context, Resources) (*Cgroup, error) {
	return nil, fmt.Errorf("cgroups v2 are only supported on Linux")
}
func (unsupportedManager) AddProcess(context.Context, *Cgroup, int) error {
	return fmt.Errorf("cgroups v2 are only supported on Linux")
}
func (unsupportedManager) Remove(context.Context, *Cgroup) error {
	return fmt.Errorf("cgroups v2 are only supported on Linux")
}
