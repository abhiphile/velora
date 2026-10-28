// Package layout reads and resolves local OCI image layouts.
package layout

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/manifest"
	"github.com/vellora/vellora/internal/image/validator"
)

const refNameAnnotation = "org.opencontainers.image.ref.name"

// Index is the OCI image-layout entry point.
type Index struct {
	SchemaVersion int                `json:"schemaVersion"`
	MediaType     string             `json:"mediaType,omitempty"`
	Manifests     []image.Descriptor `json:"manifests"`
	Annotations   map[string]string  `json:"annotations,omitempty"`
}

type layoutMarker struct {
	ImageLayoutVersion string `json:"imageLayoutVersion"`
}

// Reader validates and resolves descriptors from one local OCI layout directory.
type Reader struct {
	root   string
	index  Index
	logger *slog.Logger
}

// Open validates the required marker, index, and blob directory of an OCI layout.
func Open(root string, logger *slog.Logger) (*Reader, error) {
	if logger == nil {
		return nil, fmt.Errorf("OCI layout reader requires a logger")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve OCI layout %q: %w", root, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect OCI layout %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("OCI layout %q is not a directory", resolved)
	}
	markerPath := filepath.Join(resolved, "oci-layout")
	markerData, err := readJSONFile(markerPath, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read OCI layout marker: %w", err)
	}
	var marker layoutMarker
	if err := decodeJSON(markerData, &marker); err != nil {
		return nil, fmt.Errorf("parse OCI layout marker: %w", err)
	}
	if marker.ImageLayoutVersion != "1.0.0" {
		return nil, fmt.Errorf("unsupported OCI imageLayoutVersion %q", marker.ImageLayoutVersion)
	}
	indexPath := filepath.Join(resolved, "index.json")
	indexData, err := readJSONFile(indexPath, manifest.MaxJSONBlobSize())
	if err != nil {
		return nil, fmt.Errorf("read OCI index: %w", err)
	}
	var index Index
	if err := decodeJSON(indexData, &index); err != nil {
		return nil, fmt.Errorf("parse OCI index: %w", err)
	}
	if index.SchemaVersion != 2 {
		return nil, fmt.Errorf("OCI index schemaVersion must be 2, got %d", index.SchemaVersion)
	}
	if index.Manifests == nil {
		return nil, fmt.Errorf("OCI index manifests array is required")
	}
	if index.MediaType != "" && index.MediaType != manifest.MediaTypeIndex {
		return nil, fmt.Errorf("unsupported OCI index mediaType %q", index.MediaType)
	}
	blobDir := filepath.Join(resolved, "blobs")
	if info, err := os.Lstat(blobDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if err != nil {
			return nil, fmt.Errorf("inspect OCI blobs directory: %w", err)
		}
		return nil, fmt.Errorf("OCI blobs path %q is not a directory", blobDir)
	}
	reader := &Reader{root: resolved, index: index, logger: logger}
	logger.Info("opened OCI image layout", "path", resolved, "manifest_descriptors", len(index.Manifests))
	return reader, nil
}

// Resolve selects a reference from index.json and descends platform indexes to an image manifest.
func (reader *Reader) Resolve(ctx context.Context, reference image.Reference) (image.Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return image.Descriptor{}, fmt.Errorf("resolve OCI reference: %w", err)
	}
	selected, err := selectDescriptor(reader.index.Manifests, reference, image.Platform{OS: runtime.GOOS, Architecture: runtime.GOARCH}, true)
	if err != nil {
		return image.Descriptor{}, err
	}
	for depth := 0; selected.MediaType == manifest.MediaTypeIndex; depth++ {
		if depth >= 16 {
			return image.Descriptor{}, fmt.Errorf("OCI index nesting exceeds the maximum depth")
		}
		data, err := reader.ReadBlob(ctx, selected, manifest.MaxJSONBlobSize())
		if err != nil {
			return image.Descriptor{}, fmt.Errorf("load nested OCI index %s: %w", selected.Digest, err)
		}
		var index Index
		if err := decodeJSON(data, &index); err != nil {
			return image.Descriptor{}, fmt.Errorf("parse nested OCI index: %w", err)
		}
		if index.SchemaVersion != 2 {
			return image.Descriptor{}, fmt.Errorf("nested OCI index schemaVersion must be 2, got %d", index.SchemaVersion)
		}
		if index.MediaType != "" && index.MediaType != manifest.MediaTypeIndex {
			return image.Descriptor{}, fmt.Errorf("nested OCI index has invalid mediaType %q", index.MediaType)
		}
		if index.Manifests == nil {
			return image.Descriptor{}, fmt.Errorf("nested OCI index manifests array is required")
		}
		selected, err = selectDescriptor(index.Manifests, reference, image.Platform{OS: runtime.GOOS, Architecture: runtime.GOARCH}, false)
		if err != nil {
			return image.Descriptor{}, err
		}
	}
	if selected.MediaType != manifest.MediaTypeManifest {
		return image.Descriptor{}, fmt.Errorf("reference resolved to unsupported media type %q", selected.MediaType)
	}
	if selected.Platform != nil && ((selected.Platform.OS != "" && selected.Platform.OS != runtime.GOOS) || (selected.Platform.Architecture != "" && selected.Platform.Architecture != runtime.GOARCH)) {
		return image.Descriptor{}, fmt.Errorf("selected manifest platform %s/%s does not match host %s/%s", selected.Platform.OS, selected.Platform.Architecture, runtime.GOOS, runtime.GOARCH)
	}
	if reference.Digest != "" && selected.Digest != reference.Digest {
		return image.Descriptor{}, fmt.Errorf("reference digest %s does not match selected manifest %s", reference.Digest, selected.Digest)
	}
	reader.logger.Info("resolved OCI image reference", "reference", reference.String(), "digest", selected.Digest)
	return selected, nil
}

// ReadBlob verifies and reads a bounded blob from the layout.
func (reader *Reader) ReadBlob(ctx context.Context, descriptor image.Descriptor, maxSize int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read OCI blob: %w", err)
	}
	if err := validator.ValidateDescriptor(descriptor); err != nil {
		return nil, fmt.Errorf("validate OCI descriptor: %w", err)
	}
	if descriptor.Size > maxSize {
		return nil, fmt.Errorf("OCI blob size %d exceeds limit %d", descriptor.Size, maxSize)
	}
	path, err := BlobPath(reader.root, descriptor.Digest)
	if err != nil {
		return nil, err
	}
	file, err := reader.openBlob(path)
	if err != nil {
		return nil, fmt.Errorf("open OCI blob %q: %w", path, err)
	}
	defer file.Close()
	reader.logger.Debug("validating OCI blob digest", "digest", descriptor.Digest, "path", path)
	data, err := io.ReadAll(io.LimitReader(file, descriptor.Size+1))
	if err != nil {
		return nil, fmt.Errorf("read OCI blob %q: %w", path, err)
	}
	if err := validator.VerifyReader(bytes.NewReader(data), descriptor); err != nil {
		return nil, fmt.Errorf("verify OCI blob %s: %w", descriptor.Digest, err)
	}
	return data, nil
}

// CopyBlob copies one verified descriptor blob to a writer without buffering the full object.
func (reader *Reader) CopyBlob(ctx context.Context, descriptor image.Descriptor, destination io.Writer) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("copy OCI blob: %w", err)
	}
	if err := validator.ValidateDescriptor(descriptor); err != nil {
		return fmt.Errorf("validate OCI descriptor: %w", err)
	}
	path, err := BlobPath(reader.root, descriptor.Digest)
	if err != nil {
		return err
	}
	source, err := reader.openBlob(path)
	if err != nil {
		return fmt.Errorf("open OCI blob %q: %w", path, err)
	}
	defer source.Close()
	reader.logger.Debug("validating OCI blob digest", "digest", descriptor.Digest, "path", path)
	if err := validator.CopyVerified(destination, contextReader{ctx: ctx, reader: source}, descriptor); err != nil {
		return fmt.Errorf("copy verified OCI blob %s: %w", descriptor.Digest, err)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

// Root returns the canonical OCI layout directory.
func (reader *Reader) Root() string { return reader.root }

func (reader *Reader) openBlob(path string) (*os.File, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(reader.root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("blob path resolves outside OCI layout")
	}
	return openRegularFile(path)
}

// BlobPath converts a supported digest to its content-addressed OCI path.
func BlobPath(root, digest string) (string, error) {
	if err := validator.ValidateDigest(digest); err != nil {
		return "", fmt.Errorf("invalid OCI blob digest: %w", err)
	}
	return filepath.Join(root, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:")), nil
}

func selectDescriptor(descriptors []image.Descriptor, reference image.Reference, target image.Platform, top bool) (image.Descriptor, error) {
	var candidates []image.Descriptor
	for _, descriptor := range descriptors {
		if descriptor.MediaType != manifest.MediaTypeManifest && descriptor.MediaType != manifest.MediaTypeIndex {
			continue
		}
		if top {
			annotation := descriptor.Annotations[refNameAnnotation]
			matchesReference := annotation == reference.Tag || annotation == reference.String() || (reference.Tag == "latest" && annotation == reference.Name)
			matchesDigest := reference.Digest != "" && descriptor.Digest == reference.Digest
			if matchesReference || matchesDigest {
				candidates = append(candidates, descriptor)
			}
		} else if reference.Digest != "" && descriptor.Digest == reference.Digest {
			candidates = append(candidates, descriptor)
		} else if descriptor.Platform == nil || platformMatches(*descriptor.Platform, target) {
			candidates = append(candidates, descriptor)
		}
	}
	if len(candidates) == 0 && top && len(descriptors) == 1 && isImageDescriptor(descriptors[0]) {
		candidates = append(candidates, descriptors[0])
	}
	if len(candidates) == 0 && !top && len(descriptors) == 1 && isImageDescriptor(descriptors[0]) {
		candidates = append(candidates, descriptors[0])
	}
	if len(candidates) == 0 {
		return image.Descriptor{}, fmt.Errorf("no OCI manifest matches reference %q and platform %s/%s", reference.String(), target.OS, target.Architecture)
	}
	if len(candidates) > 1 {
		platformCandidates := candidates[:0]
		for _, descriptor := range candidates {
			if descriptor.Platform != nil && platformMatches(*descriptor.Platform, target) {
				platformCandidates = append(platformCandidates, descriptor)
			}
		}
		candidates = platformCandidates
	}
	if len(candidates) != 1 {
		return image.Descriptor{}, fmt.Errorf("reference %q is ambiguous across %d OCI descriptors", reference.String(), len(candidates))
	}
	if err := validator.ValidateDescriptor(candidates[0]); err != nil {
		return image.Descriptor{}, fmt.Errorf("invalid selected OCI descriptor: %w", err)
	}
	return candidates[0], nil
}

func platformMatches(descriptor, target image.Platform) bool {
	return descriptor.OS == target.OS && descriptor.Architecture == target.Architecture && (descriptor.Variant == "" || descriptor.Variant == target.Variant)
}

func isImageDescriptor(descriptor image.Descriptor) bool {
	return descriptor.MediaType == manifest.MediaTypeManifest || descriptor.MediaType == manifest.MediaTypeIndex
}

func readJSONFile(path string, maxSize int64) ([]byte, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("file %q exceeds %d bytes", path, maxSize)
	}
	return data, nil
}

func openRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !os.SameFile(info, openedInfo) {
		file.Close()
		return nil, fmt.Errorf("%q changed while opening", path)
	}
	return file, nil
}

func decodeJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
