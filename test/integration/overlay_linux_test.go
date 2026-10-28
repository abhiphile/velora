//go:build linux

package integration

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOverlayFSMountCopyUpAndWhiteout(t *testing.T) {
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
		t.Fatalf("make test mount tree private: %v", err)
	}
	root := t.TempDir()
	lower := filepath.Join(root, "lower")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work")
	merged := filepath.Join(root, "merged")
	for _, path := range []string{filepath.Join(lower, "etc"), upper, work, merged} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	baseFile := filepath.Join(lower, "etc", "base-file")
	if err := os.WriteFile(baseFile, []byte("base"), 0644); err != nil {
		t.Fatal(err)
	}
	options := "lowerdir=" + lower + ",upperdir=" + upper + ",workdir=" + work
	if err := unix.Mount("overlay", merged, "overlay", unix.MS_NODEV|unix.MS_NOSUID, options); err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.ENODEV) || errors.Is(err, unix.EINVAL) {
			t.Skipf("OverlayFS mount unavailable (kernel/config/capability): %v", err)
		}
		t.Fatalf("mount OverlayFS: %v", err)
	}
	t.Cleanup(func() {
		if err := unix.Unmount(merged, unix.MNT_DETACH); err != nil {
			t.Errorf("unmount OverlayFS: %v", err)
		}
	})
	containerFile := filepath.Join(merged, "etc", "base-file")
	if err := os.WriteFile(containerFile, []byte("container"), 0644); err != nil {
		t.Fatalf("write merged lower file: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(upper, "etc", "base-file")); err != nil || string(got) != "container" {
		t.Fatalf("copy-up upper contents=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(baseFile); err != nil || string(got) != "base" {
		t.Fatalf("lower layer changed: contents=%q err=%v", got, err)
	}
	if err := os.Remove(containerFile); err != nil {
		t.Fatalf("delete merged lower file: %v", err)
	}
	if _, err := os.Stat(containerFile); !os.IsNotExist(err) {
		t.Fatalf("deleted file remains in merged view, err=%v", err)
	}
	if _, err := os.Stat(baseFile); err != nil {
		t.Fatalf("whiteout changed lower file: %v", err)
	}
}
