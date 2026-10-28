//go:build linux

package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vellora/vellora/internal/storage/snapshot"
	"golang.org/x/sys/unix"
)

func TestSnapshotSiblingsHaveIndependentWritableLayers(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EINVAL) {
			t.Skipf("private mount namespace unavailable: %v", err)
		}
		t.Fatalf("unshare mount namespace: %v", err)
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		if errors.Is(err, unix.EPERM) {
			t.Skipf("cannot make test mount tree private: %v", err)
		}
		t.Fatalf("make mount tree private: %v", err)
	}
	root := t.TempDir()
	base := filepath.Join(root, "base")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	baseFile := filepath.Join(base, "shared.txt")
	if err := os.WriteFile(baseFile, []byte("base"), 0644); err != nil {
		t.Fatal(err)
	}
	manager, err := snapshot.New(filepath.Join(root, "runtime"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := manager.Prepare(ctx, "first", base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Prepare(ctx, "second", base)
	if err != nil {
		t.Fatal(err)
	}
	mounted := map[string]bool{}
	t.Cleanup(func() {
		for _, item := range []snapshot.Metadata{first, second} {
			if mounted[item.ID] {
				_ = manager.Unmount(context.Background(), item.ID)
			}
			_ = manager.Delete(context.Background(), item.ID)
		}
	})
	for _, item := range []snapshot.Metadata{first, second} {
		if _, err := manager.Mount(ctx, item.ID); err != nil {
			if errors.Is(err, unix.EPERM) || errors.Is(err, unix.ENODEV) || errors.Is(err, unix.EINVAL) {
				t.Skipf("OverlayFS mount unavailable: %v", err)
			}
			t.Fatal(err)
		}
		mounted[item.ID] = true
	}
	if err := os.WriteFile(filepath.Join(first.Paths.Merged, "shared.txt"), []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second.Paths.Merged, "shared.txt"), []byte("second"), 0644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{filepath.Join(first.Paths.Merged, "shared.txt"): "first", filepath.Join(second.Paths.Merged, "shared.txt"): "second", baseFile: "base"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s=%q, want %q", path, got, want)
		}
	}
}
