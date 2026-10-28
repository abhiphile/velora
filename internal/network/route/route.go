// Package route exposes the container default-route configuration boundary.
package route

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vellora/vellora/internal/network/namespace"
)

type Manager struct {
	configurer namespace.Configurator
	logger     *slog.Logger
}

func New(configurer namespace.Configurator, logger *slog.Logger) (*Manager, error) {
	if configurer == nil || logger == nil {
		return nil, fmt.Errorf("route manager requires namespace configurator and logger")
	}
	return &Manager{configurer: configurer, logger: logger}, nil
}
func (manager *Manager) Configure(ctx context.Context, config namespace.Config) error {
	if err := manager.configurer.Configure(ctx, config); err != nil {
		return fmt.Errorf("configure container interface and route: %w", err)
	}
	manager.logger.Info("configured container default route", "interface", "eth0", "gateway", config.Gateway)
	return nil
}
