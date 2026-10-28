package nat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type executorFunc func(context.Context, string, ...string) ([]byte, error)

func (function executorFunc) Run(ctx context.Context, program string, args ...string) ([]byte, error) {
	return function(ctx, program, args...)
}

func TestEnsureUsesOwnedChainsAndMasqueradesSubnet(t *testing.T) {
	var commands []string
	executor := executorFunc(func(_ context.Context, program string, args ...string) ([]byte, error) {
		commands = append(commands, program+" "+strings.Join(args, " "))
		if contains(args, "-C") {
			return nil, errors.New("rule absent")
		}
		if contains(args, "-S") && !contains(args, "-N") {
			return nil, errors.New("chain absent")
		}
		return nil, nil
	})
	manager, err := New(executor, "iptables", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(context.Background(), "vellora0", "10.88.0.0/16", ""); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(commands, "\n")
	for _, expected := range []string{"-w -t nat -N VELLORA_NAT", "-w -t filter -N VELLORA_FORWARD", "-s 10.88.0.0/16 ! -d 10.88.0.0/16 -j MASQUERADE", "-i vellora0 ! -o vellora0 -j ACCEPT", "--ctstate RELATED,ESTABLISHED -j ACCEPT"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("firewall calls missing %q:\n%s", expected, joined)
		}
	}
}

func contains(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}
