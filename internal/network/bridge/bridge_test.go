package bridge

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"reflect"
	"testing"
)

type recordingExecutor struct {
	calls   [][]string
	missing bool
}

func (executor *recordingExecutor) Run(_ context.Context, program string, args ...string) ([]byte, error) {
	call := append([]string{program}, args...)
	executor.calls = append(executor.calls, call)
	if executor.missing && len(args) >= 2 && args[0] == "link" && args[1] == "show" {
		return nil, io.ErrUnexpectedEOF
	}
	return nil, nil
}

func TestEnsureConfiguresBridgeForwardingAndMTU(t *testing.T) {
	executor := &recordingExecutor{missing: true}
	manager, err := New(executor, "ip", "sysctl", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	err = manager.Ensure(context.Background(), "vellora0", netip.MustParsePrefix("10.88.0.0/16"), netip.MustParseAddr("10.88.0.1"), 1450)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"ip", "link", "show", "dev", "vellora0"},
		{"ip", "link", "add", "name", "vellora0", "type", "bridge"},
		{"ip", "address", "replace", "10.88.0.1/16", "dev", "vellora0"},
		{"ip", "link", "set", "dev", "vellora0", "mtu", "1450"},
		{"ip", "link", "set", "dev", "vellora0", "up"},
		{"sysctl", "-w", "net.ipv4.ip_forward=1"},
	}
	if !reflect.DeepEqual(executor.calls, want) {
		t.Fatalf("commands = %#v, want %#v", executor.calls, want)
	}
}
