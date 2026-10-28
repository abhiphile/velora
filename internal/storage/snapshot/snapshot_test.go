package snapshot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

type fakeMounts struct {
	lower   []string
	mounted map[string]bool
}

func (f *fakeMounts) MountOverlay(lower []string, _, _, merged string) error {
	if f.mounted[merged] {
		return errors.New("already mounted")
	}
	f.lower = append([]string(nil), lower...)
	if f.mounted == nil {
		f.mounted = map[string]bool{}
	}
	f.mounted[merged] = true
	return nil
}
func (f *fakeMounts) Unmount(path string) error {
	if !f.mounted[path] {
		return errors.New("not mounted")
	}
	delete(f.mounted, path)
	return nil
}

func testManager(t *testing.T) (*Manager, *fakeMounts, string) {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "base")
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	mounts := &fakeMounts{}
	manager, err := newManager(root, newFileStore(filepath.Join(root, "snapshots")), osCleaner{}, mounts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return manager, mounts, base
}

func TestSnapshotLifecycleAndParentReferences(t *testing.T) {
	manager, mounts, base := testManager(t)
	ctx := context.Background()
	parent, err := manager.Prepare(ctx, "parent", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Mount(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unmount(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	parent, err = manager.Commit(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := manager.Prepare(ctx, "child", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := manager.Mount(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if merged != child.Paths.Merged {
		t.Fatalf("merged path=%q", merged)
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts.lower) != 2 || mounts.lower[0] != parent.Paths.Upper || mounts.lower[1] != resolvedBase {
		t.Fatalf("wrong overlay chain: %#v", mounts.lower)
	}
	if err := manager.Delete(ctx, parent.ID); err == nil {
		t.Fatal("deleted parent with child reference")
	}
	if err := manager.Unmount(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := manager.Inspect(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ReferenceCount != 0 {
		t.Fatalf("parent references=%d", updated.ReferenceCount)
	}
	if err := manager.Delete(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotMetadataSurvivesManagerRestartAndProtectsMount(t *testing.T) {
	manager, mounts, base := testManager(t)
	ctx := context.Background()
	item, err := manager.Prepare(ctx, "persisted", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Mount(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := newManager(manager.root, newFileStore(filepath.Join(manager.root, "snapshots")), osCleaner{}, mounts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.Inspect(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Mounted || loaded.ReferenceCount != 1 {
		t.Fatalf("lost mount state: %+v", loaded)
	}
	if err := restarted.Delete(ctx, item.ID); err == nil {
		t.Fatal("deleted mounted snapshot")
	}
	if err := restarted.Unmount(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRejectsInvalidParentAndID(t *testing.T) {
	manager, _, _ := testManager(t)
	if _, err := manager.Prepare(context.Background(), "../escape", "missing"); err == nil {
		t.Fatal("accepted invalid ID")
	}
	if _, err := manager.Prepare(context.Background(), "valid", "relative-parent"); err == nil {
		t.Fatal("accepted non-path, non-snapshot parent")
	}
}

func TestGarbageCollectRemovesUnreferencedChainFromLeavesUp(t *testing.T) {
	manager, _, base := testManager(t)
	ctx := context.Background()
	parent, err := manager.Prepare(ctx, "gc-parent", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Commit(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(ctx, "gc-child", parent.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := manager.GarbageCollect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("garbage collector removed %v, want both snapshots", removed)
	}
	if _, err := manager.Inspect(ctx, parent.ID); err == nil {
		t.Fatal("parent survived garbage collection")
	}
}
