package layer

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	image "github.com/vellora/vellora/internal/image"
	imageconfig "github.com/vellora/vellora/internal/image/config"
	"github.com/vellora/vellora/internal/image/manifest"
	"github.com/vellora/vellora/internal/image/testutil"
)

func TestDiscoverPreservesManifestOrderAndDiffIDs(t *testing.T) {
	manifestValue := manifest.Manifest{Layers: []image.Descriptor{
		{Digest: "sha256:" + repeated('a'), MediaType: "application/vnd.oci.image.layer.v1.tar", Size: 10},
		{Digest: "sha256:" + repeated('b'), MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Size: 20},
	}}
	configValue := imageconfig.ImageConfig{RootFS: imageconfig.RootFS{Type: "layers", DiffIDs: []string{"sha256:" + repeated('c'), "sha256:" + repeated('d')}}}
	layers, err := Discover(manifestValue, configValue)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(layers) != 2 || layers[0].Digest != manifestValue.Layers[0].Digest || layers[1].DiffID != configValue.RootFS.DiffIDs[1] {
		t.Fatalf("layer order or diff IDs lost: %+v", layers)
	}
}

func TestDiscoverRejectsLayerCountMismatch(t *testing.T) {
	if _, err := Discover(manifest.Manifest{Layers: []image.Descriptor{{}}}, imageconfig.ImageConfig{}); err == nil {
		t.Fatal("accepted mismatched layer counts")
	}
}

func TestBlobStoreOpensOnlyVerifiedLayerContent(t *testing.T) {
	fixture := testutil.Create(t, filepath.Join(t.TempDir(), "source"))
	root := filepath.Join(t.TempDir(), "store")
	blobPath := filepath.Join(root, "blobs", "sha256", strings.TrimPrefix(fixture.Layer.Digest, "sha256:"))
	if err := os.MkdirAll(filepath.Dir(blobPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, fixture.LayerBytes, 0644); err != nil {
		t.Fatal(err)
	}
	store, err := NewBlobStore(root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(context.Background(), fixture.Layer.Digest)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(data) != string(fixture.LayerBytes) {
		t.Fatal("layer blob contents changed")
	}
	if err := os.WriteFile(blobPath, []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(context.Background(), fixture.Layer.Digest); err == nil {
		t.Fatal("opened a digest-mismatched layer")
	}
}

func repeated(value byte) string { return strings.Repeat(string(value), 64) }
