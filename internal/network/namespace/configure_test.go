package namespace

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
)

type commandRecorder struct{ commands [][]string }

func (recorder *commandRecorder) Run(_ context.Context, program string, args ...string) ([]byte, error) {
	recorder.commands = append(recorder.commands, append([]string{program}, args...))
	return nil, nil
}

func TestConfigureEndpointAndDefaultRoute(t *testing.T) {
	recorder := &commandRecorder{}
	manager, err := New(recorder, "ip", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Configure(context.Background(), Config{PeerName: "vc123", Address: "10.88.0.2/16", Gateway: "10.88.0.1", MTU: 1500}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"ip", "link", "set", "dev", "lo", "up"}, {"ip", "link", "set", "dev", "vc123", "name", "eth0"}, {"ip", "link", "set", "dev", "eth0", "mtu", "1500"}, {"ip", "address", "replace", "10.88.0.2/16", "dev", "eth0"}, {"ip", "link", "set", "dev", "eth0", "up"}, {"ip", "route", "replace", "default", "via", "10.88.0.1", "dev", "eth0"}}
	if !reflect.DeepEqual(recorder.commands, want) {
		t.Fatalf("commands = %#v, want %#v", recorder.commands, want)
	}
}
