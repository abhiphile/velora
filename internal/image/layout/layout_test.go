package layout_test

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

func TestOpenResolveAndVerifyLocalLayout(t *testing.T) {
	fixture := testutil.Create(t, filepath.Join(t.TempDir(), "oci"))
	reader, err := layout.Open(fixture.Root, testLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	manifest, err := reader.Resolve(context.Background(), image.Reference{Name: "alpine", Tag: "latest"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if manifest.Digest != fixture.Manifest.Digest {
		t.Fatalf("manifest digest = %s, want %s", manifest.Digest, fixture.Manifest.Digest)
	}
	if err := reader.CopyBlob(context.Background(), fixture.Layer, io.Discard); err != nil {
		t.Fatalf("CopyBlob: %v", err)
	}
}

func TestRejectsInvalidLayoutAndDigestMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "oci-layout"), []byte(`{"imageLayoutVersion":"2.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.Open(root, testLogger()); err == nil {
		t.Fatal("accepted unsupported layout version")
	}
	fixture := testutil.Create(t, filepath.Join(t.TempDir(), "oci"))
	reader, err := layout.Open(fixture.Root, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	path, err := layout.BlobPath(fixture.Root, fixture.Layer.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered layer"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := reader.CopyBlob(context.Background(), fixture.Layer, io.Discard); err == nil {
		t.Fatal("accepted mismatched layer digest")
	}
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
