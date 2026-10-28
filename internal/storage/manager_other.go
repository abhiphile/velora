//go:build !linux

package storage

import (
	"context"
	"fmt"
	"log/slog"
)

type unsupportedManager struct{}

func newManager(Config, *slog.Logger) (RootFSManager, error) { return unsupportedManager{}, nil }

func (unsupportedManager) Prepare(context.Context, string) (RootFS, error) {
	return RootFS{}, fmt.Errorf("private root filesystems are only supported on Linux")
}

func (unsupportedManager) Enter(context.Context, RootFS) error {
	return fmt.Errorf("private root filesystems are only supported on Linux")
}
