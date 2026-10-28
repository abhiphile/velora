// Package manifest parses and validates OCI image manifests.
package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/validator"
)

const (
	MediaTypeManifest  = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeIndex     = "application/vnd.oci.image.index.v1+json"
	MediaTypeConfig    = "application/vnd.oci.image.config.v1+json"
	DockerManifest     = "application/vnd.docker.distribution.manifest.v2+json"
	DockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
	DockerConfig       = "application/vnd.docker.container.image.v1+json"
)

const maxJSONBlobSize = 16 << 20

// MaxJSONBlobSize is the maximum manifest/index/config JSON blob accepted by Vellora.
func MaxJSONBlobSize() int64 { return maxJSONBlobSize }

// Manifest is the OCI image manifest descriptor graph.
type Manifest struct {
	SchemaVersion int                `json:"schemaVersion"`
	MediaType     string             `json:"mediaType"`
	Config        image.Descriptor   `json:"config"`
	Layers        []image.Descriptor `json:"layers"`
	Annotations   map[string]string  `json:"annotations,omitempty"`
}

// Loader reads and validates one image manifest from a local OCI layout.
type Loader struct{}

// NewLoader constructs a stateless manifest loader.
func NewLoader() Loader { return Loader{} }

// Load implements the image manifest loader interface.
func (Loader) Load(ctx context.Context, layoutRoot string, descriptor image.Descriptor) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	if descriptor.MediaType != MediaTypeManifest {
		return nil, fmt.Errorf("descriptor media type %q is not an OCI image manifest", descriptor.MediaType)
	}
	if err := validator.ValidateDescriptor(descriptor); err != nil {
		return nil, fmt.Errorf("validate manifest descriptor: %w", err)
	}
	if descriptor.Size > maxJSONBlobSize {
		return nil, fmt.Errorf("manifest size %d exceeds limit %d", descriptor.Size, maxJSONBlobSize)
	}
	path, err := blobPath(layoutRoot, descriptor.Digest)
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(layoutRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve layout root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve manifest blob path: %w", err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("manifest blob resolves outside OCI layout")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect manifest blob %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("manifest blob %q is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open manifest blob %q: %w", path, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened manifest blob: %w", err)
	}
	if !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("manifest blob changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, descriptor.Size+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest blob %q: %w", path, err)
	}
	if err := validator.VerifyReader(bytes.NewReader(data), descriptor); err != nil {
		return nil, fmt.Errorf("verify manifest blob: %w", err)
	}
	return data, nil
}

// Parse validates the required OCI manifest structure and supported media types.
func Parse(data []byte) (Manifest, error) {
	var parsed Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&parsed); err != nil {
		return Manifest{}, fmt.Errorf("decode OCI manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("OCI manifest contains trailing JSON values")
		}
		return Manifest{}, fmt.Errorf("decode trailing OCI manifest data: %w", err)
	}
	if parsed.SchemaVersion != 2 {
		return Manifest{}, fmt.Errorf("manifest schemaVersion must be 2, got %d", parsed.SchemaVersion)
	}
	if parsed.MediaType != MediaTypeManifest && parsed.MediaType != DockerManifest {
		return Manifest{}, fmt.Errorf("unsupported image manifest mediaType %q", parsed.MediaType)
	}
	if parsed.Layers == nil {
		return Manifest{}, fmt.Errorf("manifest layers array is required")
	}
	if parsed.Config.MediaType != MediaTypeConfig && parsed.Config.MediaType != DockerConfig {
		return Manifest{}, fmt.Errorf("unsupported image config mediaType %q", parsed.Config.MediaType)
	}
	if err := validator.ValidateDescriptor(parsed.Config); err != nil {
		return Manifest{}, fmt.Errorf("invalid config descriptor: %w", err)
	}
	for index, layer := range parsed.Layers {
		if !isLayerMediaType(layer.MediaType) {
			return Manifest{}, fmt.Errorf("layer %d has unsupported OCI media type %q", index, layer.MediaType)
		}
		if err := validator.ValidateDescriptor(layer); err != nil {
			return Manifest{}, fmt.Errorf("invalid layer descriptor %d: %w", index, err)
		}
	}
	return parsed, nil
}

func isLayerMediaType(value string) bool {
	switch value {
	case "application/vnd.oci.image.layer.v1.tar", "application/vnd.oci.image.layer.v1.tar+gzip", "application/vnd.oci.image.layer.v1.tar+zstd",
		"application/vnd.docker.image.rootfs.diff.tar.gzip", "application/vnd.docker.image.rootfs.diff.tar",
		"application/vnd.oci.image.layer.nondistributable.v1.tar", "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip", "application/vnd.oci.image.layer.nondistributable.v1.tar+zstd":
		return true
	default:
		return false
	}
}

func blobPath(root, digest string) (string, error) {
	if err := validator.ValidateDigest(digest); err != nil {
		return "", fmt.Errorf("invalid blob digest: %w", err)
	}
	return filepath.Join(root, "blobs", "sha256", digest[len("sha256:"):]), nil
}

var _ image.ManifestLoader = Loader{}
