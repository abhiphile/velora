// Package nat installs Vellora-owned forwarding and masquerade firewall rules.
package nat

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vellora/vellora/internal/network/command"
	"github.com/vellora/vellora/internal/network/firewall"
)

const natChain = "VELLORA_NAT"
const forwardChain = "VELLORA_FORWARD"

type Manager struct {
	executor firewall.Runner
	logger   *slog.Logger
}

func New(executor command.Executor, binary string, logger *slog.Logger) (*Manager, error) {
	if executor == nil || logger == nil {
		return nil, fmt.Errorf("NAT manager requires executor and logger")
	}
	runner, err := firewall.New(executor, binary)
	if err != nil {
		return nil, err
	}
	return &Manager{executor: runner, logger: logger}, nil
}

func (manager *Manager) Ensure(ctx context.Context, bridge, subnet, _ string) error {
	if bridge == "" || subnet == "" {
		return fmt.Errorf("bridge and subnet are required for NAT")
	}
	if err := manager.ensureChain(ctx, "nat", natChain); err != nil {
		return err
	}
	if err := manager.ensureChain(ctx, "filter", forwardChain); err != nil {
		return err
	}
	for _, rule := range [][]string{{"-t", "nat", "-C", "POSTROUTING", "-j", natChain}, {"-t", "filter", "-C", "FORWARD", "-j", forwardChain}} {
		if _, err := manager.executor.Run(ctx, rule...); err != nil {
			add := append([]string(nil), rule...)
			add[2] = "-A"
			if _, addErr := manager.executor.Run(ctx, add...); addErr != nil {
				return fmt.Errorf("attach Vellora firewall chain: %w", addErr)
			}
		}
	}
	for _, rule := range [][]string{{"-t", "nat", "-C", natChain, "-s", subnet, "!", "-d", subnet, "-j", "MASQUERADE"}, {"-t", "filter", "-C", forwardChain, "-i", bridge, "!", "-o", bridge, "-j", "ACCEPT"}, {"-t", "filter", "-C", forwardChain, "-o", bridge, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}} {
		if _, err := manager.executor.Run(ctx, rule...); err != nil {
			add := append([]string(nil), rule...)
			add[2] = "-A"
			if _, addErr := manager.executor.Run(ctx, add...); addErr != nil {
				return fmt.Errorf("install Vellora firewall rule: %w", addErr)
			}
		}
	}
	manager.logger.Info("configured host NAT and forwarding rules", "bridge", bridge, "subnet", subnet)
	return nil
}

func (manager *Manager) ensureChain(ctx context.Context, table, name string) error {
	if _, err := manager.executor.Run(ctx, "-t", table, "-S", name); err == nil {
		return nil
	}
	if _, err := manager.executor.Run(ctx, "-t", table, "-N", name); err != nil {
		if _, checkErr := manager.executor.Run(ctx, "-t", table, "-S", name); checkErr != nil {
			return fmt.Errorf("create firewall chain %s: %w", name, err)
		}
	}
	return nil
}

func (manager *Manager) Remove(ctx context.Context, bridge string) error {
	for _, chain := range []struct{ table, chain, jump string }{{"nat", natChain, "POSTROUTING"}, {"filter", forwardChain, "FORWARD"}} {
		check := []string{"-w", "-t", chain.table, "-C", chain.jump, "-j", chain.chain}
		if _, err := manager.executor.Run(ctx, check...); err == nil {
			deleteArgs := append([]string(nil), check...)
			deleteArgs[3] = "-D"
			if _, err := manager.executor.Run(ctx, deleteArgs...); err != nil {
				return fmt.Errorf("remove firewall jump to %s: %w", chain.chain, err)
			}
		}
		if _, err := manager.executor.Run(ctx, "-t", chain.table, "-S", chain.chain); err != nil {
			continue
		}
		if _, err := manager.executor.Run(ctx, "-t", chain.table, "-F", chain.chain); err != nil {
			return fmt.Errorf("flush Vellora firewall chain %s: %w", chain.chain, err)
		}
		if _, err := manager.executor.Run(ctx, "-t", chain.table, "-X", chain.chain); err != nil {
			return fmt.Errorf("delete Vellora firewall chain %s: %w", chain.chain, err)
		}
	}
	manager.logger.Info("removed Vellora-owned NAT rules", "bridge", bridge)
	return nil
}

var _ interface {
	Ensure(context.Context, string, string, string) error
	Remove(context.Context, string) error
} = (*Manager)(nil)
