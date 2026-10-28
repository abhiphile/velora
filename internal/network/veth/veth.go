// Package veth manages point-to-point virtual Ethernet links.
package veth

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vellora/vellora/internal/network/command"
)

type Manager struct {
	executor command.Executor
	ip       string
	logger   *slog.Logger
}

func New(executor command.Executor, ipBinary string, logger *slog.Logger) (*Manager, error) {
	if executor == nil || logger == nil {
		return nil, fmt.Errorf("veth manager requires executor and logger")
	}
	if ipBinary == "" {
		ipBinary = "ip"
	}
	return &Manager{executor: executor, ip: ipBinary, logger: logger}, nil
}
func (manager *Manager) CreateAndMove(ctx context.Context, hostName, peerName, bridge string, pid, mtu int) error {
	if pid <= 0 || hostName == "" || peerName == "" || bridge == "" || mtu < 576 {
		return fmt.Errorf("invalid veth configuration")
	}
	if _, err := manager.executor.Run(ctx, manager.ip, "link", "add", hostName, "type", "veth", "peer", "name", peerName); err != nil {
		return fmt.Errorf("create veth pair %s/%s: %w", hostName, peerName, err)
	}
	rollback := func(cause error) error {
		if _, err := manager.executor.Run(context.Background(), manager.ip, "link", "delete", hostName); err != nil {
			return fmt.Errorf("%w; rollback veth: %v", cause, err)
		}
		return cause
	}
	for _, args := range [][]string{{"link", "set", "dev", hostName, "master", bridge}, {"link", "set", "dev", hostName, "mtu", fmt.Sprint(mtu)}, {"link", "set", "dev", hostName, "up"}, {"link", "set", "dev", peerName, "netns", fmt.Sprint(pid)}} {
		if _, err := manager.executor.Run(ctx, manager.ip, args...); err != nil {
			return rollback(fmt.Errorf("configure veth pair: %w", err))
		}
	}
	manager.logger.Info("created and moved veth endpoint", "host_veth", hostName, "container_veth", peerName, "pid", pid, "bridge", bridge)
	return nil
}
func (manager *Manager) Delete(ctx context.Context, hostName string) error {
	if hostName == "" {
		return fmt.Errorf("host veth name is required")
	}
	if _, err := manager.executor.Run(ctx, manager.ip, "link", "delete", hostName); err != nil {
		return fmt.Errorf("delete host veth %q: %w", hostName, err)
	}
	manager.logger.Info("deleted veth pair", "host_veth", hostName)
	return nil
}
