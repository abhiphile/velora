//go:build linux

package overlay

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareAndCleanupPreserveUpperAndReleaseLayer(t *testing.T) {
	root := t.TempDir()
	lower := filepath.Join(root, "images", "layers", "alpine")
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.MkdirAll(lower, 0755); err != nil {
		t.Fatal(err)
	}
	layers := &fakeLayerManager{path: lower}
	mounts := &fakeMountManager{}
	manager := newCore(Config{ImagesRoot: filepath.Join(root, "images"), RuntimeRoot: runtimeRoot}, testLogger(), layers, mounts)
	paths, err := manager.Prepare(context.Background(), "container-1", "alpine")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if paths.Lower != lower || paths.Upper != filepath.Join(runtimeRoot, "containers", "container-1", "upper") || paths.Work != filepath.Join(runtimeRoot, "containers", "container-1", "work") || paths.Merged != filepath.Join(runtimeRoot, "containers", "container-1", "merged") {
		t.Fatalf("unexpected paths: %+v", paths)
	}
	if err := os.WriteFile(filepath.Join(paths.Upper, "saved"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Mount(context.Background(), paths); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if !mounts.private || mounts.lower != lower || mounts.upper != paths.Upper || mounts.work != paths.Work || mounts.merged != paths.Merged {
		t.Fatalf("unexpected mount arguments: %+v", mounts)
	}
	if err := manager.Cleanup(context.Background(), paths); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(paths.Merged); !os.IsNotExist(err) {
		t.Fatalf("merged mountpoint still exists, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.Upper, "saved")); err != nil {
		t.Fatalf("upper layer was not preserved: %v", err)
	}
	if layers.references != 0 {
		t.Fatalf("active layer references = %d, want 0", layers.references)
	}
}

func TestPrepareRejectsLayerTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	images := filepath.Join(root, "images")
	layersRoot := filepath.Join(images, "layers")
	if err := os.MkdirAll(layersRoot, 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(layersRoot, "escape")); err != nil {
		t.Fatal(err)
	}
	manager := newCore(Config{ImagesRoot: images, RuntimeRoot: filepath.Join(root, "runtime")}, testLogger(), &directoryLayers{root: layersRoot, references: map[string]int{}}, &fakeMountManager{})
	if _, err := manager.Prepare(context.Background(), "id", "../outside"); err == nil {
		t.Fatal("accepted traversing layer name")
	}
	if _, err := manager.Prepare(context.Background(), "id", "escape"); err == nil {
		t.Fatal("accepted layer symlink escaping layers root")
	}
}

func TestPrepareRejectsReusedContainerDirectory(t *testing.T) {
	root := t.TempDir()
	lower := filepath.Join(root, "images", "layers", "base")
	if err := os.MkdirAll(lower, 0755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "runtime", "containers", "id")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	manager := newCore(Config{ImagesRoot: filepath.Join(root, "images"), RuntimeRoot: filepath.Join(root, "runtime")}, testLogger(), &fakeLayerManager{path: lower}, &fakeMountManager{})
	if _, err := manager.Prepare(context.Background(), "id", "base"); err == nil {
		t.Fatal("accepted an existing container directory")
	}
	if manager.layers.(*fakeLayerManager).references != 0 {
		t.Fatal("failed prepare leaked a layer reference")
	}
}

func TestMountRejectsOptionDelimiters(t *testing.T) {
	if err := (unixMounts{}).MountOverlay("/lower,redirect_dir=on", "/upper", "/work", "/merged"); err == nil {
		t.Fatal("accepted a mount option delimiter")
	}
}

type fakeLayerManager struct {
	path       string
	references int
}

func (manager *fakeLayerManager) Resolve(_ context.Context, name string) (string, error) {
	if !validName(name) {
		return "", os.ErrInvalid
	}
	return manager.path, nil
}
func (manager *fakeLayerManager) Acquire(context.Context, string) error {
	manager.references++
	return nil
}
func (manager *fakeLayerManager) Release(context.Context, string) error {
	manager.references--
	return nil
}

type fakeMountManager struct {
	lower, upper, work, merged string
	private                    bool
}

func (manager *fakeMountManager) MakeMountsPrivate() error { manager.private = true; return nil }

func (manager *fakeMountManager) MountOverlay(lower, upper, work, merged string) error {
	manager.lower, manager.upper, manager.work, manager.merged = lower, upper, work, merged
	return nil
}
func (*fakeMountManager) Unmount(string) error { return nil }

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
