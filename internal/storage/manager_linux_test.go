//go:build linux

package storage

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrepareCanonicalizesAndValidatesRootFS(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"proc", "tmp", "dev"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	manager := newLinuxManager(DefaultConfig(), slog.New(slog.NewTextHandler(discardWriter{}, nil)), osFilesystem{}, &recordingMountManager{})
	prepared, err := manager.Prepare(context.Background(), root)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared.Path != root || !prepared.ReadOnly {
		t.Fatalf("prepared rootfs = %+v, want path %q and read-only", prepared, root)
	}
	if _, err := manager.Prepare(context.Background(), "/"); err == nil {
		t.Fatal("host root was accepted as a container rootfs")
	}
}

func TestEnterAppliesRootfsMountsInSafeOrder(t *testing.T) {
	mounts := &recordingMountManager{}
	files := &fakeFilesystem{}
	manager := newLinuxManager(DefaultConfig(), slog.New(slog.NewTextHandler(discardWriter{}, nil)), files, mounts)
	if err := manager.Enter(context.Background(), RootFS{Path: "/fixture/root", ReadOnly: true}); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	want := []string{
		"mount:/|flags=MS_REC|MS_PRIVATE",
		"mount:/fixture/root|source=/fixture/root|flags=MS_BIND",
		"pivot:.:.",
		"unmount:.",
		"mount:/|flags=MS_BIND|MS_REMOUNT|MS_RDONLY",
		"mount:/proc|type=proc",
		"mount:/tmp|type=tmpfs",
		"mount:/dev|type=tmpfs",
		"mount:/dev/pts|type=devpts",
		"mount:/dev/shm|type=tmpfs",
	}
	if !reflect.DeepEqual(mounts.calls, want) {
		t.Fatalf("mount operations =\n%v\nwant\n%v", mounts.calls, want)
	}
	if !reflect.DeepEqual(files.chdirCalls, []string{"/fixture/root", "/"}) {
		t.Fatalf("chdir calls = %v, want rootfs then new root", files.chdirCalls)
	}
}

func TestEnterRollsBackMountsWhenLaterSetupFails(t *testing.T) {
	mounts := &recordingMountManager{failTarget: "/dev"}
	manager := newLinuxManager(DefaultConfig(), slog.New(slog.NewTextHandler(discardWriter{}, nil)), &fakeFilesystem{}, mounts)
	err := manager.Enter(context.Background(), RootFS{Path: "/fixture/root", ReadOnly: true})
	if err == nil {
		t.Fatal("expected mount failure")
	}
	if !containsCall(mounts.calls, "unmount:/tmp") || !containsCall(mounts.calls, "unmount:/proc") {
		t.Fatalf("mounts were not rolled back in reverse order: %v", mounts.calls)
	}
}

type fakeFilesystem struct{ chdirCalls []string }

func (fakeFilesystem) EvalSymlinks(value string) (string, error) { return value, nil }
func (fakeFilesystem) Stat(string) (fs.FileInfo, error)          { return nil, errors.New("not used") }
func (fakeFilesystem) MkdirAll(string, fs.FileMode) error        { return nil }
func (filesystem *fakeFilesystem) Chdir(value string) error {
	filesystem.chdirCalls = append(filesystem.chdirCalls, value)
	return nil
}
func (fakeFilesystem) Symlink(string, string) error    { return nil }
func (fakeFilesystem) Mknod(string, uint32, int) error { return nil }

type recordingMountManager struct {
	calls      []string
	failTarget string
}

func (mounts *recordingMountManager) Mount(source, target, filesystem string, flags uintptr, data string) error {
	call := "mount:" + target
	if source != "" {
		call = "mount:" + target + "|source=" + source
	}
	if filesystem != "" {
		call += "|type=" + filesystem
	}
	if data != "" {
		call += "|data=" + data
	}
	if flags != 0 {
		call += "|flags=" + flagNames(flags)
	}
	mounts.calls = append(mounts.calls, call)
	if target == mounts.failTarget {
		return errors.New("simulated mount error")
	}
	return nil
}

func (mounts *recordingMountManager) PivotRoot(newRoot, putOld string) error {
	mounts.calls = append(mounts.calls, "pivot:"+newRoot+":"+putOld)
	return nil
}

func (mounts *recordingMountManager) Unmount(target string, _ int) error {
	mounts.calls = append(mounts.calls, "unmount:"+target)
	return nil
}

func flagNames(flags uintptr) string {
	var names []string
	for _, item := range []struct {
		flag uintptr
		name string
	}{{unix.MS_REC, "MS_REC"}, {unix.MS_PRIVATE, "MS_PRIVATE"}, {unix.MS_BIND, "MS_BIND"}, {unix.MS_REMOUNT, "MS_REMOUNT"}, {unix.MS_RDONLY, "MS_RDONLY"}} {
		flag, name := item.flag, item.name
		if flags&flag != 0 {
			names = append(names, name)
		}
	}
	result := ""
	for index, name := range names {
		if index > 0 {
			result += "|"
		}
		result += name
	}
	return result
}

func containsCall(calls []string, target string) bool {
	for _, call := range calls {
		if call == target {
			return true
		}
	}
	return false
}

type discardWriter struct{}

func (discardWriter) Write(value []byte) (int, error) { return len(value), nil }
