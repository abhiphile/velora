//go:build linux && integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"log/slog"
	"net/netip"

	"github.com/vellora/vellora/internal/network"
)

// This integration test modifies host networking. Run it only on a disposable Linux VM:
// VELLORA_NETWORK_INTEGRATION=1 go test -tags=integration ./test/integration -run TestBridgeLifecycle
func TestBridgeLifecycle(t *testing.T) {
	if os.Getenv("VELLORA_NETWORK_INTEGRATION") != "1" {
		t.Skip("set VELLORA_NETWORK_INTEGRATION=1 in a disposable Linux VM")
	}
	if os.Geteuid() != 0 {
		t.Skip("bridge integration test requires root")
	}
	for _, binary := range []string{"ip", "iptables", "sysctl"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is required: %v", binary, err)
		}
	}
	ctx := context.Background()
	oldForward, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		t.Fatal(err)
	}
	bridgeName := "vlnettest0"
	if err := exec.Command("ip", "link", "show", "dev", bridgeName).Run(); err == nil {
		t.Skipf("bridge %s already exists; refusing to alter it", bridgeName)
	}
	manager, err := network.NewManager(network.Config{RuntimeRoot: filepath.Join(t.TempDir(), "state"), Bridge: bridgeName, Subnet: netip.MustParsePrefix("198.18.88.0/24"), Gateway: netip.MustParseAddr("198.18.88.1"), DNS: netip.MustParseAddr("8.8.8.8"), MTU: 1500}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := manager.Remove(context.Background(), bridgeName); removeErr != nil {
			t.Logf("remove integration network: %v", removeErr)
		}
		if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", oldForward, 0644); err != nil {
			t.Logf("restore ip_forward: %v", err)
		}
	})
	if err := manager.Create(ctx, bridgeName); err != nil {
		t.Fatal(fmt.Errorf("create bridge network: %w", err))
	}
	info, err := manager.Inspect(ctx, bridgeName)
	if err != nil {
		t.Fatal(err)
	}
	if info.Bridge != bridgeName || info.Subnet != "198.18.88.0/24" {
		t.Fatalf("unexpected network inspection: %+v", info)
	}
}
