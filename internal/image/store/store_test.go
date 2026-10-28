package store

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/layout"
	"github.com/vellora/vellora/internal/image/testutil"
)

func TestImportResolveListAndDeleteLocalOCIImage(t *testing.T) {
	fixture := testutil.Create(t, filepath.Join(t.TempDir(), "source-layout"))
	storeRoot := filepath.Join(t.TempDir(), "images")
	store, err := NewLocal(storeRoot, testLogger())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	ctx := context.Background()
	created, err := store.Create(ctx, image.Reference{Name: "alpine"}, fixture.Root)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Architecture != "amd64" || created.OS != "linux" || created.WorkingDirectory != "/tmp" || len(created.Entrypoint) != 1 || len(created.Layers) != 1 {
		t.Fatalf("unexpected image metadata: %+v", created)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, "blobs", "sha256")); err != nil {
		t.Fatalf("blob store missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, "cache")); err != nil {
		t.Fatalf("cache directory missing: %v", err)
	}
	resolved, err := store.Load(ctx, image.Reference{Name: "alpine"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if resolved.ManifestDigest != fixture.Manifest.Digest || resolved.Reference != "alpine:latest" {
		t.Fatalf("unexpected resolved image: %+v", resolved)
	}
	if _, err := store.Create(ctx, image.Reference{Name: "alpine", Tag: "3.20"}, fixture.Root); err != nil {
		t.Fatalf("create alias tag: %v", err)
	}
	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 || listed[0].Reference != "alpine:3.20" || listed[1].Reference != "alpine:latest" {
		t.Fatalf("unexpected image list: %+v", listed)
	}
	if err := store.Delete(ctx, image.Reference{Name: "alpine", Tag: "latest"}); err != nil {
		t.Fatalf("Delete latest: %v", err)
	}
	if _, err := store.Load(ctx, image.Reference{Name: "alpine", Tag: "latest"}); err == nil {
		t.Fatal("deleted tag still resolves")
	}
	if _, err := store.Load(ctx, image.Reference{Name: "alpine", Tag: "3.20"}); err != nil {
		t.Fatalf("remaining tag no longer resolves: %v", err)
	}
	if err := store.Delete(ctx, image.Reference{Name: "alpine", Tag: "3.20"}); err != nil {
		t.Fatalf("Delete 3.20: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, "blobs", "sha256", fixture.Layer.Digest[len("sha256:"):])); err != nil {
		t.Fatalf("shared immutable layer blob was deleted: %v", err)
	}
}

func TestImportRejectsLayerDigestMismatch(t *testing.T) {
	fixture := testutil.Create(t, filepath.Join(t.TempDir(), "source-layout"))
	path, err := layout.BlobPath(fixture.Root, fixture.Layer.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocal(filepath.Join(t.TempDir(), "images"), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), image.Reference{Name: "alpine"}, fixture.Root); err == nil {
		t.Fatal("import accepted a layer digest mismatch")
	}
	images, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("failed import registered image: %+v", images)
	}
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
