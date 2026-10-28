// Package testutil creates deterministic OCI image layouts for package tests.
package testutil

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	image "github.com/vellora/vellora/internal/image"
	imageconfig "github.com/vellora/vellora/internal/image/config"
	"github.com/vellora/vellora/internal/image/layout"
	"github.com/vellora/vellora/internal/image/manifest"
)

// Fixture describes a minimal valid local OCI image layout.
type Fixture struct {
	Root       string
	Manifest   image.Descriptor
	Config     image.Descriptor
	Layer      image.Descriptor
	LayerBytes []byte
}

// Create builds a small uncompressed OCI image layout tagged as latest.
func Create(t *testing.T, root string) Fixture {
	t.Helper()
	blobRoot := filepath.Join(root, "blobs", "sha256")
	if err := os.MkdirAll(blobRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	var layerBuffer bytes.Buffer
	tarWriter := tar.NewWriter(&layerBuffer)
	content := []byte("NAME=fixture\n")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "etc/os-release", Mode: 0644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	layerBytes := layerBuffer.Bytes()
	layer := writeBlob(t, blobRoot, "application/vnd.oci.image.layer.v1.tar", layerBytes)
	rootHash := sha256.Sum256(layerBytes)
	imageConfig := imageconfig.ImageConfig{
		Architecture: "amd64", OS: "linux",
		Config: imageconfig.Runtime{Entrypoint: []string{"/bin/sh"}, Cmd: []string{"-c", "echo fixture"}, Env: []string{"PATH=/bin"}, WorkingDir: "/tmp", Labels: map[string]string{"example": "test"}},
		RootFS: imageconfig.RootFS{Type: "layers", DiffIDs: []string{"sha256:" + hex.EncodeToString(rootHash[:])}},
	}
	configBytes, err := json.Marshal(imageConfig)
	if err != nil {
		t.Fatal(err)
	}
	config := writeBlob(t, blobRoot, imageconfig.MediaType, configBytes)
	manifestValue := manifest.Manifest{SchemaVersion: 2, MediaType: manifest.MediaTypeManifest, Config: config, Layers: []image.Descriptor{layer}}
	manifestBytes, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifestDescriptor := writeBlob(t, blobRoot, manifest.MediaTypeManifest, manifestBytes)
	index := layout.Index{SchemaVersion: 2, MediaType: manifest.MediaTypeIndex, Manifests: []image.Descriptor{{MediaType: manifest.MediaTypeManifest, Digest: manifestDescriptor.Digest, Size: manifestDescriptor.Size, Annotations: map[string]string{"org.opencontainers.image.ref.name": "latest"}}}}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), indexBytes, 0644); err != nil {
		t.Fatal(err)
	}
	return Fixture{Root: root, Manifest: manifestDescriptor, Config: config, Layer: layer, LayerBytes: layerBytes}
}

func writeBlob(t *testing.T, root, mediaType string, data []byte) image.Descriptor {
	t.Helper()
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	path := filepath.Join(root, hex.EncodeToString(sum[:]))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return image.Descriptor{MediaType: mediaType, Digest: digest, Size: int64(len(data))}
}
