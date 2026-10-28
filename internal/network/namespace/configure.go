// Package namespace configures the endpoint after it has entered a container network namespace.
package namespace

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vellora/vellora/internal/network/command"
)

type Config struct {
	PeerName, Address, Gateway string
	MTU                        int
}
type Configurator interface {
	Configure(context.Context, Config) error
}
type Manager struct {
	executor command.Executor
	ip       string
	logger   *slog.Logger
}

func New(executor command.Executor, ipBinary string, logger *slog.Logger) (*Manager, error) {
	if executor == nil || logger == nil {
		return nil, fmt.Errorf("network namespace manager requires executor and logger")
	}
	if ipBinary == "" {
		ipBinary = "ip"
	}
	return &Manager{executor: executor, ip: ipBinary, logger: logger}, nil
}
func (manager *Manager) Configure(ctx context.Context, config Config) error {
	if config.PeerName == "" || config.Address == "" || config.Gateway == "" || config.MTU < 576 {
		return fmt.Errorf("invalid container network configuration")
	}
	commands := [][]string{{"link", "set", "dev", "lo", "up"}, {"link", "set", "dev", config.PeerName, "name", "eth0"}, {"link", "set", "dev", "eth0", "mtu", fmt.Sprint(config.MTU)}, {"address", "replace", config.Address, "dev", "eth0"}, {"link", "set", "dev", "eth0", "up"}, {"route", "replace", "default", "via", config.Gateway, "dev", "eth0"}}
	for _, args := range commands {
		if _, err := manager.executor.Run(ctx, manager.ip, args...); err != nil {
			return fmt.Errorf("configure container network (%v): %w", args, err)
		}
	}
	manager.logger.Info("configured container network interface", "interface", "eth0", "address", config.Address, "gateway", config.Gateway)
	return nil
}

var _ Configurator = (*Manager)(nil)
