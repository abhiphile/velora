// Package layer discovers manifest layers and provides verified local blob access.
package layer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	image "github.com/vellora/vellora/internal/image"
	imageconfig "github.com/vellora/vellora/internal/image/config"
	"github.com/vellora/vellora/internal/image/manifest"
	"github.com/vellora/vellora/internal/image/validator"
)

// Discover converts ordered manifest descriptors and config DiffIDs into layer metadata.
func Discover(manifestValue manifest.Manifest, configValue imageconfig.ImageConfig) ([]image.Layer, error) {
	if len(manifestValue.Layers) != len(configValue.RootFS.DiffIDs) {
		return nil, fmt.Errorf("manifest layer count %d does not match config diff_id count %d", len(manifestValue.Layers), len(configValue.RootFS.DiffIDs))
	}
	layers := make([]image.Layer, len(manifestValue.Layers))
	for index, descriptor := range manifestValue.Layers {
		layers[index] = image.Layer{Digest: descriptor.Digest, DiffID: configValue.RootFS.DiffIDs[index], MediaType: descriptor.MediaType, Size: descriptor.Size}
	}
	return layers, nil
}

// BlobStore provides access to locally stored, content-addressed image layers.
type BlobStore struct {
	root   string
	logger *slog.Logger
}

// NewBlobStore creates a layer reader rooted at an image content store.
func NewBlobStore(root string, logger *slog.Logger) (*BlobStore, error) {
	if logger == nil {
		return nil, fmt.Errorf("layer store requires a logger")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve layer store root: %w", err)
	}
	return &BlobStore{root: absolute, logger: logger}, nil
}

// Open verifies a layer's SHA-256 content before returning a rewound reader.
func (store *BlobStore) Open(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open layer blob: %w", err)
	}
	path, err := blobPath(store.root, digest)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open layer blob %q: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("inspect layer blob %q: %w", path, err)
	}
	descriptor := image.Descriptor{MediaType: "application/octet-stream", Digest: digest, Size: info.Size()}
	if err := validator.VerifyReader(file, descriptor); err != nil {
		file.Close()
		return nil, fmt.Errorf("verify layer blob %s: %w", digest, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, fmt.Errorf("rewind layer blob %q: %w", path, err)
	}
	store.logger.Debug("discovered verified OCI layer", "digest", digest, "size", info.Size())
	return file, nil
}

func blobPath(root, digest string) (string, error) {
	if err := validator.ValidateDigest(digest); err != nil {
		return "", fmt.Errorf("invalid layer digest: %w", err)
	}
	return filepath.Join(root, "blobs", "sha256", digest[len("sha256:"):]), nil
}

var _ image.LayerStore = (*BlobStore)(nil)
