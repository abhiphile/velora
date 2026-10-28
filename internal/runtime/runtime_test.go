package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"testing"

	"github.com/vellora/vellora/internal/cgroup"
	"github.com/vellora/vellora/internal/namespace"
	"github.com/vellora/vellora/internal/network"
	"github.com/vellora/vellora/internal/process"
	"github.com/vellora/vellora/internal/storage"
	"github.com/vellora/vellora/internal/storage/overlay"
)

func TestRunJoinsProcessBeforeWaitingAndRemovesCgroup(t *testing.T) {
	events := []string{}
	runner := &fakeProcessRunner{events: &events}
	cgroups := &fakeCgroupManager{events: &events}
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), runner, cgroups, fakeStorageManager{events: &events})
	resources := cgroup.Resources{MemoryBytes: 512 << 20, CPUCores: 1, PIDsMax: 64}
	if err := manager.Run(context.Background(), process.Spec{Args: []string{"/bin/sh"}}, namespace.Set{Mount: true}, resources); err != nil {
		t.Fatal(err)
	}
	want := []string{"create", "join:1234", "run", "remove"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if cgroups.received != resources {
		t.Fatalf("resources = %+v, want %+v", cgroups.received, resources)
	}
}

func TestRunRemovesCgroupWhenProcessFails(t *testing.T) {
	events := []string{}
	runner := &fakeProcessRunner{events: &events, runErr: errors.New("process failed")}
	cgroups := &fakeCgroupManager{events: &events}
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), runner, cgroups, fakeStorageManager{events: &events})
	err := manager.Run(context.Background(), process.Spec{Args: []string{"/bin/sh"}}, namespace.Set{Mount: true}, cgroup.Resources{PIDsMax: 4})
	if err == nil {
		t.Fatal("expected process failure")
	}
	if events[len(events)-1] != "remove" {
		t.Fatalf("cgroup was not removed after failure: %v", events)
	}
}

func TestSetEnvironmentReplacesDuplicateValues(t *testing.T) {
	got := setEnvironment([]string{"PATH=/bin", "PWD=/host", "PWD=/stale"}, "PWD", "/")
	if !reflect.DeepEqual(got, []string{"PATH=/bin", "PWD=/"}) {
		t.Fatalf("environment = %v, want one PWD=/ entry", got)
	}
}

func TestRunPreparesAndCleansOverlayAroundProcess(t *testing.T) {
	events := []string{}
	runner := &fakeProcessRunner{events: &events}
	overlays := &fakeOverlayManager{events: &events}
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), runner, nil, fakeStorageManager{events: &events}, overlays)
	if err := manager.Run(context.Background(), process.Spec{Args: []string{"/bin/sh"}, RootFS: "alpine"}, namespace.Set{Mount: true}, cgroup.Resources{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"prepare-overlay", "run", "cleanup-overlay"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if runner.received.RootFS != "/runtime/containers/test/merged" || runner.received.OverlayLower != "/images/layers/alpine" {
		t.Fatalf("runner spec did not receive overlay paths: %+v", runner.received)
	}
}

func TestRunConnectsNetworkBeforeWorkloadAndDisconnectsAfterward(t *testing.T) {
	events := []string{}
	runner := &fakeProcessRunner{events: &events}
	networkManager := &fakeNetworkManager{events: &events}
	manager := NewManagerWithNetwork(slog.New(slog.NewTextHandler(io.Discard, nil)), runner, nil, fakeStorageManager{events: &events}, nil, networkManager)
	if err := manager.Run(context.Background(), process.Spec{Args: []string{"/bin/sh"}}, namespace.Set{Mount: true}, cgroup.Resources{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"prepare-network", "connect:1234", "run", "disconnect"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if runner.received.NetworkConfig == nil || runner.received.NetworkConfig.ID != "net-test" {
		t.Fatalf("runner did not receive network configuration: %+v", runner.received.NetworkConfig)
	}
}

type fakeStorageManager struct{ events *[]string }

func (manager fakeStorageManager) Prepare(_ context.Context, path string) (storage.RootFS, error) {
	*manager.events = append(*manager.events, "prepare-rootfs")
	return storage.RootFS{Path: path}, nil
}

func (manager fakeStorageManager) Enter(context.Context, storage.RootFS) error {
	*manager.events = append(*manager.events, "enter-rootfs")
	return nil
}

type fakeProcessRunner struct {
	events   *[]string
	runErr   error
	received process.Spec
}

func (runner *fakeProcessRunner) Run(_ context.Context, spec process.Spec, _ namespace.Set, beforeExec func(int) error) error {
	runner.received = spec
	if beforeExec != nil {
		if err := beforeExec(1234); err != nil {
			return err
		}
	}
	*runner.events = append(*runner.events, "run")
	return runner.runErr
}

type fakeOverlayManager struct{ events *[]string }

func (manager *fakeOverlayManager) Prepare(_ context.Context, _, layer string) (overlay.Paths, error) {
	*manager.events = append(*manager.events, "prepare-overlay")
	return overlay.Paths{LayerName: layer, Lower: "/images/layers/" + layer, Upper: "/runtime/containers/test/upper", Work: "/runtime/containers/test/work", Merged: "/runtime/containers/test/merged"}, nil
}

type fakeNetworkManager struct{ events *[]string }

func (manager *fakeNetworkManager) Prepare(_ context.Context, id string) (network.Attachment, error) {
	*manager.events = append(*manager.events, "prepare-network")
	return network.Attachment{Config: network.ContainerConfig{ID: "net-test"}, HostVeth: "vh-test"}, nil
}
func (manager *fakeNetworkManager) Connect(_ context.Context, _ network.Attachment, pid int) error {
	*manager.events = append(*manager.events, "connect:"+strconv.Itoa(pid))
	return nil
}
func (*fakeNetworkManager) ConfigureContainer(context.Context, network.ContainerConfig) error {
	return nil
}
func (*fakeNetworkManager) ConfigureDNS(context.Context, network.ContainerConfig, string) error {
	return nil
}
func (manager *fakeNetworkManager) Disconnect(context.Context, network.Attachment) error {
	*manager.events = append(*manager.events, "disconnect")
	return nil
}
func (*fakeNetworkManager) List(context.Context) ([]network.NetworkInfo, error) { return nil, nil }
func (*fakeNetworkManager) Inspect(context.Context, string) (network.NetworkInfo, error) {
	return network.NetworkInfo{}, nil
}
func (*fakeNetworkManager) Create(context.Context, string) error       { return nil }
func (*fakeNetworkManager) Remove(context.Context, string) error       { return nil }
func (*fakeOverlayManager) Mount(context.Context, overlay.Paths) error { return nil }
func (manager *fakeOverlayManager) Cleanup(context.Context, overlay.Paths) error {
	*manager.events = append(*manager.events, "cleanup-overlay")
	return nil
}

type fakeCgroupManager struct {
	events   *[]string
	received cgroup.Resources
}

func (manager *fakeCgroupManager) Create(_ context.Context, resources cgroup.Resources) (*cgroup.Cgroup, error) {
	manager.received = resources
	*manager.events = append(*manager.events, "create")
	return &cgroup.Cgroup{}, nil
}

func (manager *fakeCgroupManager) AddProcess(_ context.Context, _ *cgroup.Cgroup, pid int) error {
	*manager.events = append(*manager.events, "join:"+strconv.Itoa(pid))
	return nil
}

func (manager *fakeCgroupManager) Remove(_ context.Context, _ *cgroup.Cgroup) error {
	*manager.events = append(*manager.events, "remove")
	return nil
}
