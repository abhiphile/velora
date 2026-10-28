// Package bridge manages the host-side Linux Ethernet bridge.
package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/vellora/vellora/internal/network/command"
)

type Manager struct {
	executor   command.Executor
	ip, sysctl string
	logger     *slog.Logger
}

func New(executor command.Executor, ipBinary, sysctlBinary string, logger *slog.Logger) (*Manager, error) {
	if executor == nil || logger == nil {
		return nil, fmt.Errorf("bridge manager requires executor and logger")
	}
	if ipBinary == "" {
		ipBinary = "ip"
	}
	if sysctlBinary == "" {
		sysctlBinary = "sysctl"
	}
	return &Manager{executor: executor, ip: ipBinary, sysctl: sysctlBinary, logger: logger}, nil
}
func (manager *Manager) Ensure(ctx context.Context, name string, subnet netip.Prefix, gateway netip.Addr, mtu int) error {
	if name == "" || !subnet.IsValid() || !gateway.IsValid() || !subnet.Contains(gateway) || mtu < 576 {
		return fmt.Errorf("invalid bridge configuration")
	}
	if _, err := manager.executor.Run(ctx, manager.ip, "link", "show", "dev", name); err != nil {
		if _, createErr := manager.executor.Run(ctx, manager.ip, "link", "add", "name", name, "type", "bridge"); createErr != nil {
			return fmt.Errorf("create bridge %q: %w", name, createErr)
		}
		manager.logger.Info("created Linux bridge", "bridge", name)
	} else {
		manager.logger.Debug("using existing Linux bridge", "bridge", name)
	}
	address := netip.PrefixFrom(gateway, subnet.Bits()).String()
	if _, err := manager.executor.Run(ctx, manager.ip, "address", "replace", address, "dev", name); err != nil {
		return fmt.Errorf("configure bridge address %s: %w", address, err)
	}
	if _, err := manager.executor.Run(ctx, manager.ip, "link", "set", "dev", name, "mtu", fmt.Sprint(mtu)); err != nil {
		return fmt.Errorf("configure bridge MTU: %w", err)
	}
	if _, err := manager.executor.Run(ctx, manager.ip, "link", "set", "dev", name, "up"); err != nil {
		return fmt.Errorf("bring bridge up: %w", err)
	}
	if _, err := manager.executor.Run(ctx, manager.sysctl, "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	return nil
}
func (manager *Manager) Delete(ctx context.Context, name string) error {
	if _, err := manager.executor.Run(ctx, manager.ip, "link", "delete", name, "type", "bridge"); err != nil {
		return fmt.Errorf("delete bridge %q: %w", name, err)
	}
	manager.logger.Info("deleted Linux bridge", "bridge", name)
	return nil
}
func (manager *Manager) Exists(ctx context.Context, name string) bool {
	_, err := manager.executor.Run(ctx, manager.ip, "link", "show", "dev", name)
	return err == nil
}
