// Package store implements a local OCI content-addressed image store.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	image "github.com/vellora/vellora/internal/image"
	imageconfig "github.com/vellora/vellora/internal/image/config"
	"github.com/vellora/vellora/internal/image/layer"
	"github.com/vellora/vellora/internal/image/layout"
	"github.com/vellora/vellora/internal/image/manifest"
	"github.com/vellora/vellora/internal/image/reference"
	"github.com/vellora/vellora/internal/image/validator"
)

// Local stores validated image metadata, references, and immutable blobs on disk.
type Local struct {
	root      string
	logger    *slog.Logger
	parser    image.ReferenceParser
	manifests image.ManifestLoader
	mu        sync.RWMutex
}

type referenceRecord struct {
	Reference      string `json:"reference"`
	ManifestDigest string `json:"manifest_digest"`
}
type metadataRecord struct {
	Image      image.Image `json:"image"`
	References []string    `json:"references"`
}

// NewLocal initializes the configured blob, metadata, cache, and reference directories.
func NewLocal(root string, logger *slog.Logger) (*Local, error) {
	if logger == nil {
		return nil, fmt.Errorf("image store requires a logger")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve image store root: %w", err)
	}
	store := &Local{root: abs, logger: logger, parser: reference.NewParser(), manifests: manifest.NewLoader()}
	for _, directory := range []string{store.blobRoot(), filepath.Join(abs, "metadata"), filepath.Join(abs, "cache"), filepath.Join(abs, "refs")} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			return nil, fmt.Errorf("create image store directory %q: %w", directory, err)
		}
	}
	return store, nil
}

// Create imports and validates one image from an existing local OCI layout.
func (store *Local) Create(ctx context.Context, requested image.Reference, layoutPath string) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return image.Image{}, fmt.Errorf("create local image: %w", err)
	}
	parsed, err := store.parser.Parse(requested.String())
	if err != nil {
		return image.Image{}, fmt.Errorf("validate image reference: %w", err)
	}
	store.logger.Info("loading local OCI image", "reference", parsed.String(), "layout", layoutPath)
	reader, err := layout.Open(layoutPath, store.logger)
	if err != nil {
		return image.Image{}, fmt.Errorf("open source OCI layout: %w", err)
	}
	manifestDescriptor, err := reader.Resolve(ctx, parsed)
	if err != nil {
		return image.Image{}, fmt.Errorf("resolve image in OCI layout: %w", err)
	}
	if parsed.Digest != "" && parsed.Digest != manifestDescriptor.Digest {
		return image.Image{}, fmt.Errorf("requested digest %s does not match layout manifest %s", parsed.Digest, manifestDescriptor.Digest)
	}
	store.logger.Info("reading OCI image manifest", "reference", parsed.String(), "digest", manifestDescriptor.Digest)
	manifestData, err := store.manifests.Load(ctx, reader.Root(), manifestDescriptor)
	if err != nil {
		return image.Image{}, fmt.Errorf("read image manifest: %w", err)
	}
	parsedManifest, err := manifest.Parse(manifestData)
	if err != nil {
		return image.Image{}, fmt.Errorf("validate image manifest: %w", err)
	}
	configData, err := reader.ReadBlob(ctx, parsedManifest.Config, manifest.MaxJSONBlobSize())
	if err != nil {
		return image.Image{}, fmt.Errorf("read image config: %w", err)
	}
	parsedConfig, err := imageconfig.Parse(configData, len(parsedManifest.Layers))
	if err != nil {
		return image.Image{}, fmt.Errorf("validate image config: %w", err)
	}
	if manifestDescriptor.Platform != nil && (manifestDescriptor.Platform.OS != "" && manifestDescriptor.Platform.OS != parsedConfig.OS || manifestDescriptor.Platform.Architecture != "" && manifestDescriptor.Platform.Architecture != parsedConfig.Architecture) {
		return image.Image{}, fmt.Errorf("manifest platform %s/%s disagrees with config %s/%s", manifestDescriptor.Platform.OS, manifestDescriptor.Platform.Architecture, parsedConfig.OS, parsedConfig.Architecture)
	}
	layers, err := layer.Discover(parsedManifest, parsedConfig)
	if err != nil {
		return image.Image{}, err
	}
	for index, discovered := range layers {
		store.logger.Debug("discovered OCI image layer", "index", index, "digest", discovered.Digest, "media_type", discovered.MediaType, "size", discovered.Size)
	}
	result := summarize(parsed, manifestDescriptor, parsedManifest, parsedConfig, layers)
	for _, descriptor := range append([]image.Descriptor{manifestDescriptor, parsedManifest.Config}, parsedManifest.Layers...) {
		if err := store.installBlob(ctx, reader, descriptor); err != nil {
			return image.Image{}, fmt.Errorf("store image blob %s: %w", descriptor.Digest, err)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.register(parsed, result); err != nil {
		return image.Image{}, fmt.Errorf("register image reference: %w", err)
	}
	result.Reference = parsed.String()
	store.logger.Info("created local OCI image", "reference", parsed.String(), "manifest", result.ManifestDigest, "layers", len(result.Layers))
	return result, nil
}

// Load resolves and returns validated metadata for a tag or manifest digest.
func (store *Local) Load(ctx context.Context, requested image.Reference) (image.Image, error) {
	return store.Resolve(ctx, requested)
}

// ImportDownloaded validates and installs blobs fetched by an OCI Distribution client.
// blobFiles maps descriptor digests to verified or untrusted staging files; every file is verified again here.
func (store *Local) ImportDownloaded(ctx context.Context, requested image.Reference, manifestDescriptor image.Descriptor, manifestData []byte, blobFiles map[string]string) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return image.Image{}, fmt.Errorf("import downloaded image: %w", err)
	}
	parsed, err := store.parser.Parse(requested.String())
	if err != nil {
		return image.Image{}, fmt.Errorf("validate image reference: %w", err)
	}
	if err := validator.VerifyReader(bytes.NewReader(manifestData), manifestDescriptor); err != nil {
		return image.Image{}, fmt.Errorf("verify downloaded manifest: %w", err)
	}
	parsedManifest, err := manifest.Parse(manifestData)
	if err != nil {
		return image.Image{}, fmt.Errorf("validate downloaded manifest: %w", err)
	}
	if parsedManifest.MediaType != manifestDescriptor.MediaType {
		return image.Image{}, fmt.Errorf("manifest body media type %q disagrees with descriptor %q", parsedManifest.MediaType, manifestDescriptor.MediaType)
	}
	if parsedManifest.Config.Size > manifest.MaxJSONBlobSize() {
		return image.Image{}, fmt.Errorf("downloaded image config exceeds JSON size limit")
	}
	configPath, ok := blobFiles[parsedManifest.Config.Digest]
	if !ok {
		return image.Image{}, fmt.Errorf("downloaded config %s is missing", parsedManifest.Config.Digest)
	}
	if err := validator.VerifyFile(configPath, parsedManifest.Config); err != nil {
		return image.Image{}, fmt.Errorf("verify downloaded config: %w", err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return image.Image{}, fmt.Errorf("read downloaded config: %w", err)
	}
	parsedConfig, err := imageconfig.Parse(configData, len(parsedManifest.Layers))
	if err != nil {
		return image.Image{}, fmt.Errorf("validate downloaded config: %w", err)
	}
	layers, err := layer.Discover(parsedManifest, parsedConfig)
	if err != nil {
		return image.Image{}, fmt.Errorf("discover downloaded layers: %w", err)
	}
	if manifestDescriptor.Platform != nil && ((manifestDescriptor.Platform.OS != "" && manifestDescriptor.Platform.OS != parsedConfig.OS) || (manifestDescriptor.Platform.Architecture != "" && manifestDescriptor.Platform.Architecture != parsedConfig.Architecture)) {
		return image.Image{}, fmt.Errorf("selected manifest platform disagrees with image config")
	}
	item := summarize(parsed, manifestDescriptor, parsedManifest, parsedConfig, layers)
	descriptors := append([]image.Descriptor{manifestDescriptor, parsedManifest.Config}, parsedManifest.Layers...)
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, descriptor := range descriptors {
		if err := ctx.Err(); err != nil {
			return image.Image{}, fmt.Errorf("install downloaded image blobs: %w", err)
		}
		source, ok := blobFiles[descriptor.Digest]
		if !ok {
			return image.Image{}, fmt.Errorf("downloaded blob %s is missing", descriptor.Digest)
		}
		if err := store.installFile(ctx, descriptor, source); err != nil {
			return image.Image{}, fmt.Errorf("install downloaded blob %s: %w", descriptor.Digest, err)
		}
	}
	if err := store.register(parsed, item); err != nil {
		return image.Image{}, fmt.Errorf("register downloaded image: %w", err)
	}
	item.Reference = parsed.String()
	store.logger.Info("imported downloaded OCI image", "reference", parsed.String(), "manifest", manifestDescriptor.Digest, "layers", len(layers))
	return item, nil
}

func (store *Local) installFile(ctx context.Context, descriptor image.Descriptor, source string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := store.blobPath(descriptor.Digest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return fmt.Errorf("create blob directory: %w", err)
	}
	if info, statErr := os.Lstat(target); statErr == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return fmt.Errorf("existing blob path %q is not a regular file", target)
	}
	if err := validator.VerifyFile(target, descriptor); err == nil {
		return nil
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open staged blob: %w", err)
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(target), ".blob-*-")
	if err != nil {
		return fmt.Errorf("create blob temp file: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0644); err != nil {
		temporary.Close()
		return fmt.Errorf("set blob permissions: %w", err)
	}
	if err := validator.CopyVerified(temporary, contextReader{ctx: ctx, reader: input}, descriptor); err != nil {
		temporary.Close()
		return fmt.Errorf("verify staged blob: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync staged blob: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close staged blob: %w", err)
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("install blob: %w", err)
	}
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("open blob directory for sync: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("sync blob directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close blob directory: %w", err)
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

// Resolve implements local reference resolution.
func (store *Local) Resolve(ctx context.Context, requested image.Reference) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return image.Image{}, fmt.Errorf("resolve local image: %w", err)
	}
	parsed, err := store.parser.Parse(requested.String())
	if err != nil {
		return image.Image{}, fmt.Errorf("validate image reference: %w", err)
	}
	store.logger.Info("resolving local image reference", "reference", parsed.String())
	store.mu.RLock()
	defer store.mu.RUnlock()
	manifestDigest := parsed.Digest
	if manifestDigest == "" {
		record, err := readJSON[referenceRecord](store.referencePath(parsed))
		if err != nil {
			return image.Image{}, fmt.Errorf("resolve image tag %q: %w", parsed.String(), err)
		}
		if record.Reference != parsed.String() {
			return image.Image{}, fmt.Errorf("reference metadata mismatch for %q", parsed.String())
		}
		manifestDigest = record.ManifestDigest
	} else if record, err := readJSON[referenceRecord](store.referencePath(parsed)); err == nil {
		if record.Reference != parsed.String() {
			return image.Image{}, fmt.Errorf("reference metadata mismatch for %q", parsed.String())
		}
		manifestDigest = record.ManifestDigest
	} else if !errors.Is(err, os.ErrNotExist) {
		return image.Image{}, fmt.Errorf("resolve digest reference %q: %w", parsed.String(), err)
	}
	if err := validator.ValidateDigest(manifestDigest); err != nil {
		return image.Image{}, fmt.Errorf("invalid manifest digest in local reference: %w", err)
	}
	metadata, err := readJSON[metadataRecord](store.metadataPath(manifestDigest))
	if err != nil {
		return image.Image{}, fmt.Errorf("load image metadata for %s: %w", manifestDigest, err)
	}
	if metadata.Image.ManifestDigest != manifestDigest {
		return image.Image{}, fmt.Errorf("metadata manifest digest mismatch for %s", manifestDigest)
	}
	result := metadata.Image
	result.Reference = parsed.String()
	return result, nil
}

// List returns all locally registered tagged references in stable order.
func (store *Local) List(ctx context.Context) ([]image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list local images: %w", err)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	entries, err := os.ReadDir(filepath.Join(store.root, "refs"))
	if err != nil {
		return nil, fmt.Errorf("read local image references: %w", err)
	}
	images := make([]image.Image, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("list local images: %w", err)
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		record, err := readJSON[referenceRecord](filepath.Join(store.root, "refs", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read image reference %q: %w", entry.Name(), err)
		}
		if err := validator.ValidateDigest(record.ManifestDigest); err != nil {
			return nil, fmt.Errorf("invalid manifest digest in reference %q: %w", record.Reference, err)
		}
		parsed, err := store.parser.Parse(record.Reference)
		if err != nil || parsed.String() != record.Reference {
			return nil, fmt.Errorf("invalid stored image reference %q", record.Reference)
		}
		metadata, err := readJSON[metadataRecord](store.metadataPath(record.ManifestDigest))
		if err != nil {
			return nil, fmt.Errorf("read image metadata for %q: %w", record.Reference, err)
		}
		item := metadata.Image
		item.Reference = record.Reference
		images = append(images, item)
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Reference < images[j].Reference })
	return images, nil
}

// Delete removes a local tag reference and unreferenced image metadata; immutable blobs remain shared.
func (store *Local) Delete(ctx context.Context, requested image.Reference) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete local image: %w", err)
	}
	parsed, err := store.parser.Parse(requested.String())
	if err != nil {
		return fmt.Errorf("validate image reference: %w", err)
	}
	if parsed.Digest != "" {
		return fmt.Errorf("delete requires a tagged image reference, not a digest")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	referencePath := store.referencePath(parsed)
	record, err := readJSON[referenceRecord](referencePath)
	if err != nil {
		return fmt.Errorf("load image reference %q: %w", parsed.String(), err)
	}
	if record.Reference != parsed.String() {
		return fmt.Errorf("image reference metadata mismatch for %q", parsed.String())
	}
	if err := validator.ValidateDigest(record.ManifestDigest); err != nil {
		return fmt.Errorf("invalid manifest digest in reference: %w", err)
	}
	metadataPath := store.metadataPath(record.ManifestDigest)
	metadata, err := readJSON[metadataRecord](metadataPath)
	if err != nil {
		return fmt.Errorf("load image metadata for deletion: %w", err)
	}
	metadata.References = removeReference(metadata.References, parsed.String())
	if len(metadata.References) > 0 {
		if err := writeJSONAtomic(metadataPath, metadata); err != nil {
			return fmt.Errorf("update image metadata references: %w", err)
		}
	}
	if err := os.Remove(referencePath); err != nil {
		return fmt.Errorf("remove image reference %q: %w", parsed.String(), err)
	}
	if len(metadata.References) == 0 {
		if err := os.Remove(metadataPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove image metadata: %w", err)
		}
	}
	store.logger.Info("deleted local image reference", "reference", parsed.String(), "manifest", record.ManifestDigest)
	return nil
}

func summarize(reference image.Reference, manifestDescriptor image.Descriptor, parsedManifest manifest.Manifest, parsedConfig imageconfig.ImageConfig, layers []image.Layer) image.Image {
	return image.Image{Reference: reference.String(), ManifestDigest: manifestDescriptor.Digest, ConfigDigest: parsedManifest.Config.Digest,
		Architecture: parsedConfig.Architecture, OS: parsedConfig.OS, Variant: parsedConfig.Variant,
		Entrypoint: parsedConfig.Config.Entrypoint, Command: parsedConfig.Config.Cmd, Environment: parsedConfig.Config.Env,
		WorkingDirectory: parsedConfig.Config.WorkingDir, User: parsedConfig.Config.User, Labels: parsedConfig.Config.Labels, Layers: layers}
}

func (store *Local) installBlob(ctx context.Context, reader *layout.Reader, descriptor image.Descriptor) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("install image blob: %w", err)
	}
	target, err := store.blobPath(descriptor.Digest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return fmt.Errorf("create blob directory: %w", err)
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("existing blob path %q is not a regular file", target)
		}
		if err := validator.VerifyFile(target, descriptor); err == nil {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect existing blob %q: %w", target, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".blob-*-")
	if err != nil {
		return fmt.Errorf("create temporary blob: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0644); err != nil {
		temporary.Close()
		return fmt.Errorf("set blob mode: %w", err)
	}
	if err := reader.CopyBlob(ctx, descriptor, temporary); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary blob: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary blob: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("install blob at %q: %w", target, err)
	}
	if directory, err := os.Open(filepath.Dir(target)); err == nil {
		if syncErr := directory.Sync(); syncErr != nil {
			directory.Close()
			return fmt.Errorf("sync blob directory: %w", syncErr)
		}
		if closeErr := directory.Close(); closeErr != nil {
			return fmt.Errorf("close blob directory: %w", closeErr)
		}
	} else {
		return fmt.Errorf("open blob directory for sync: %w", err)
	}
	return nil
}

func (store *Local) register(reference image.Reference, item image.Image) error {
	refPath := store.referencePath(reference)
	metadataPath := store.metadataPath(item.ManifestDigest)
	var previousMetadata *metadataRecord
	var previousDigest string
	if previous, err := readJSON[referenceRecord](refPath); err == nil && previous.ManifestDigest != item.ManifestDigest {
		if err := validator.ValidateDigest(previous.ManifestDigest); err != nil {
			return fmt.Errorf("invalid previous reference digest: %w", err)
		}
		oldMetadata, err := readJSON[metadataRecord](store.metadataPath(previous.ManifestDigest))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read previous image metadata: %w", err)
		}
		if err == nil {
			previousDigest = previous.ManifestDigest
			oldMetadata.References = removeReference(oldMetadata.References, reference.String())
			previousMetadata = &oldMetadata
			if err := writeJSONAtomic(store.metadataPath(previous.ManifestDigest), oldMetadata); err != nil {
				return err
			}
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read previous image reference: %w", err)
	}
	metadata, err := readJSON[metadataRecord](metadataPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		metadata = metadataRecord{Image: item}
	}
	metadata.Image = item
	metadata.Image.Reference = ""
	metadata.References = appendUnique(metadata.References, reference.String())
	if err := writeJSONAtomic(metadataPath, metadata); err != nil {
		return err
	}
	if err := writeJSONAtomic(refPath, referenceRecord{Reference: reference.String(), ManifestDigest: item.ManifestDigest}); err != nil {
		return err
	}
	if previousMetadata != nil && len(previousMetadata.References) == 0 {
		if err := os.Remove(store.metadataPath(previousDigest)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove replaced image metadata: %w", err)
		}
	}
	return nil
}

func (store *Local) blobRoot() string { return filepath.Join(store.root, "blobs", "sha256") }
func (store *Local) blobPath(digest string) (string, error) {
	if err := validator.ValidateDigest(digest); err != nil {
		return "", fmt.Errorf("invalid blob digest: %w", err)
	}
	return filepath.Join(store.blobRoot(), strings.TrimPrefix(digest, "sha256:")), nil
}
func (store *Local) metadataPath(digest string) string {
	return filepath.Join(store.root, "metadata", strings.TrimPrefix(digest, "sha256:")+".json")
}
func (store *Local) referencePath(reference image.Reference) string {
	sum := sha256.Sum256([]byte(reference.String()))
	return filepath.Join(store.root, "refs", hex.EncodeToString(sum[:])+".json")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
func removeReference(values []string, value string) []string {
	result := values[:0]
	for _, existing := range values {
		if existing != value {
			result = append(result, existing)
		}
	}
	return result
}

func readJSON[T any](path string) (T, error) {
	var value T
	data, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode JSON %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return value, fmt.Errorf("JSON %q has trailing data", path)
	}
	return value, nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON metadata: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".metadata-*")
	if err != nil {
		return fmt.Errorf("create metadata temp file: %w", err)
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return fmt.Errorf("write metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync metadata: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close metadata: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace metadata %q: %w", path, err)
	}
	return nil
}

var _ image.ImageStore = (*Local)(nil)
var _ image.ImageResolver = (*Local)(nil)
