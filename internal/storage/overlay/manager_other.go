//go:build !linux

package overlay

import (
	"context"
	"fmt"
	"log/slog"
)

func newManager(Config, *slog.Logger) (OverlayManager, error) {
	return nil, fmt.Errorf("OverlayFS is only supported on Linux")
}

func (*Manager) Prepare(context.Context, string, string) (Paths, error) {
	return Paths{}, fmt.Errorf("OverlayFS is only supported on Linux")
}

func (*Manager) Mount(context.Context, Paths) error {
	return fmt.Errorf("OverlayFS is only supported on Linux")
}

func (*Manager) Cleanup(context.Context, Paths) error {
	return fmt.Errorf("OverlayFS is only supported on Linux")
}
