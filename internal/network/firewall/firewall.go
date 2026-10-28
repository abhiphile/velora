// Package firewall provides a narrow, shell-free interface to host firewall tools.
package firewall

import (
	"context"
	"fmt"

	"github.com/vellora/vellora/internal/network/command"
)

type Runner interface {
	Run(context.Context, ...string) ([]byte, error)
}

type Manager struct {
	executor command.Executor
	binary   string
}

func New(executor command.Executor, binary string) (*Manager, error) {
	if executor == nil {
		return nil, fmt.Errorf("firewall manager requires an executor")
	}
	if binary == "" {
		binary = "iptables"
	}
	return &Manager{executor: executor, binary: binary}, nil
}

func (manager *Manager) Run(ctx context.Context, args ...string) ([]byte, error) {
	if manager == nil || manager.executor == nil {
		return nil, fmt.Errorf("firewall manager is not configured")
	}
	return manager.executor.Run(ctx, manager.binary, append([]string{"-w"}, args...)...)
}
