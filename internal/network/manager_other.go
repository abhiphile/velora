//go:build !linux

package network

import (
	"fmt"
	"log/slog"
)

func NewManager(Config, *slog.Logger) (NetworkManager, error) {
	return nil, fmt.Errorf("container networking is only supported on Linux")
}
