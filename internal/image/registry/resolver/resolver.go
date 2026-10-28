// Package resolver coordinates registry manifests, platform selection, and blob downloads.
package resolver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	image "github.com/vellora/vellora/internal/image"
	imageconfig "github.com/vellora/vellora/internal/image/config"
	"github.com/vellora/vellora/internal/image/manifest"
	"github.com/vellora/vellora/internal/image/reference"
	"github.com/vellora/vellora/internal/image/registry/auth"
	"github.com/vellora/vellora/internal/image/registry/blob"
	"github.com/vellora/vellora/internal/image/registry/cache"
	"github.com/vellora/vellora/internal/image/registry/client"
	"github.com/vellora/vellora/internal/image/registry/download"
	registryManifest "github.com/vellora/vellora/internal/image/registry/manifest"
	"github.com/vellora/vellora/internal/image/registry/validator"
)

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

type RegistryClient interface {
	Ping(context.Context, string, string) error
	Get(context.Context, string, string, string, int64) (*http.Response, error)
}
type BlobDownloader interface {
	DownloadMany(context.Context, []image.Descriptor, download.Fetcher) (map[string]string, error)
}
type ImageResolver interface {
	Pull(context.Context, string) (image.Image, error)
}
type Config struct {
	ImageRoot          string
	Platform           image.Platform
	MaxParallel        int
	InsecureRegistries []string
}

type Resolver struct {
	config     Config
	parser     image.ReferenceParser
	local      image.ImageResolver
	installer  image.DownloadedImageStore
	client     RegistryClient
	downloader BlobDownloader
	cache      cache.Cache
	logger     *slog.Logger
}

func New(config Config, local image.ImageResolver, installer image.DownloadedImageStore, httpClient *http.Client, logger *slog.Logger) (*Resolver, error) {
	if config.ImageRoot == "" || local == nil || installer == nil || logger == nil {
		return nil, fmt.Errorf("registry resolver requires image root, local resolver, installer, and logger")
	}
	if config.Platform.OS == "" {
		config.Platform.OS = runtime.GOOS
	}
	if config.Platform.Architecture == "" {
		config.Platform.Architecture = runtime.GOARCH
	}
	if config.MaxParallel <= 0 {
		config.MaxParallel = 4
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Minute}
	}
	tokens := authProvider(httpClient, logger)
	registryClient, err := client.New(httpClient, tokens, logger)
	if err != nil {
		return nil, err
	}
	blobCache, err := cache.New(filepath.Join(config.ImageRoot, "cache", "registry"))
	if err != nil {
		return nil, fmt.Errorf("configure registry cache: %w", err)
	}
	downloader, err := download.New(blobCache, config.MaxParallel, logger)
	if err != nil {
		return nil, fmt.Errorf("configure blob downloader: %w", err)
	}
	return newResolver(config, local, installer, registryClient, downloader, blobCache, logger), nil
}

func newResolver(config Config, local image.ImageResolver, installer image.DownloadedImageStore, registryClient RegistryClient, downloader BlobDownloader, blobCache cache.Cache, logger *slog.Logger) *Resolver {
	return &Resolver{config: config, parser: reference.NewParser(), local: local, installer: installer, client: registryClient, downloader: downloader, cache: blobCache, logger: logger}
}

func authProvider(httpClient *http.Client, _ *slog.Logger) client.TokenProvider {
	return auth.NewBearerProvider(httpClient, auth.DockerConfig{})
}

func (resolver *Resolver) Pull(ctx context.Context, value string) (image.Image, error) {
	requested, err := resolver.parser.Parse(value)
	if err != nil {
		return image.Image{}, fmt.Errorf("parse remote image reference: %w", err)
	}
	if local, err := resolver.local.Resolve(ctx, requested); err == nil {
		resolver.logger.Info("image already available locally", "reference", requested.String())
		return local, nil
	}
	registryHost, repository, tag := normalize(requested)
	base := "https://" + registryHost
	if isInsecure(registryHost, resolver.config.InsecureRegistries) {
		base = "http://" + registryHost
	}
	scope := "repository:" + repository + ":pull"
	resolver.logger.Info("pinging image registry", "registry", registryHost, "repository", repository)
	if err := resolver.client.Ping(ctx, base, scope); err != nil {
		return image.Image{}, err
	}
	manifestURL, err := blob.ManifestEndpoint(base, repository, requested.Digest)
	if requested.Digest == "" {
		manifestURL, err = blob.ManifestEndpoint(base, repository, tag)
	}
	if err != nil {
		return image.Image{}, err
	}
	data, contentType, headerDigest, err := resolver.getManifest(ctx, manifestURL, scope)
	if err != nil {
		return image.Image{}, fmt.Errorf("fetch image manifest: %w", err)
	}
	digest := requested.Digest
	if digest == "" {
		digest = sha256Digest(data)
	}
	if headerDigest != "" && headerDigest != digest {
		return image.Image{}, fmt.Errorf("registry manifest digest header %s disagrees with content %s", headerDigest, digest)
	}
	manifestDescriptor := image.Descriptor{MediaType: contentType, Digest: digest, Size: int64(len(data))}
	if err := validator.VerifyReader(bytes.NewReader(data), manifestDescriptor); err != nil {
		return image.Image{}, fmt.Errorf("verify registry manifest: %w", err)
	}
	if contentType == manifest.MediaTypeIndex || contentType == manifest.DockerManifestList {
		index, err := registryManifest.ParseIndex(data)
		if err != nil {
			return image.Image{}, fmt.Errorf("parse image index: %w", err)
		}
		selected, err := registryManifest.Select(index, resolver.config.Platform)
		if err != nil {
			return image.Image{}, err
		}
		manifestURL, err = blob.ManifestEndpoint(base, repository, selected.Digest)
		if err != nil {
			return image.Image{}, err
		}
		data, contentType, _, err = resolver.getManifest(ctx, manifestURL, scope)
		if err != nil {
			return image.Image{}, fmt.Errorf("fetch platform manifest: %w", err)
		}
		if contentType != selected.MediaType {
			return image.Image{}, fmt.Errorf("selected descriptor media type %q disagrees with response %q", selected.MediaType, contentType)
		}
		if err := validator.VerifyReader(bytes.NewReader(data), selected); err != nil {
			return image.Image{}, fmt.Errorf("verify platform manifest: %w", err)
		}
		manifestDescriptor = selected
	} else {
		if requested.Digest != "" && manifestDescriptor.Digest != requested.Digest {
			return image.Image{}, fmt.Errorf("requested digest mismatch")
		}
	}
	parsed, err := registryManifest.Parse(data, contentType)
	if err != nil {
		return image.Image{}, fmt.Errorf("parse image manifest: %w", err)
	}
	if parsed.Descriptor.Digest != manifestDescriptor.Digest || parsed.Descriptor.Size != manifestDescriptor.Size {
		return image.Image{}, fmt.Errorf("manifest descriptor does not match manifest content")
	}
	resolver.logger.Info("fetched image manifest", "digest", manifestDescriptor.Digest, "media_type", contentType)
	if parsed.Manifest.Config.Size > manifest.MaxJSONBlobSize() {
		return image.Image{}, fmt.Errorf("image config exceeds JSON size limit")
	}
	descriptors := append([]image.Descriptor{parsed.Manifest.Config}, parsed.Manifest.Layers...)
	paths := make(map[string]string, len(descriptors)+1)
	paths[manifestDescriptor.Digest], err = resolver.cacheManifest(ctx, manifestDescriptor, data)
	if err != nil {
		return image.Image{}, err
	}
	missing := make([]image.Descriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if path, ok := resolver.existingBlob(descriptor); ok {
			paths[descriptor.Digest] = path
			resolver.logger.Info("reusing local image blob", "digest", descriptor.Digest)
		} else {
			missing = append(missing, descriptor)
		}
	}
	responsePaths, err := resolver.downloader.DownloadMany(ctx, missing, func(callCtx context.Context, descriptor image.Descriptor, offset int64) (cache.FetchResponse, error) {
		endpoint, err := blob.Endpoint(base, repository, descriptor.Digest)
		if err != nil {
			return cache.FetchResponse{}, err
		}
		response, err := resolver.client.Get(callCtx, endpoint, scope, "", offset)
		if err != nil {
			return cache.FetchResponse{}, err
		}
		return cache.FetchResponse{Body: response.Body, StatusCode: response.StatusCode, ContentRange: response.Header.Get("Content-Range")}, nil
	})
	if err != nil {
		return image.Image{}, fmt.Errorf("download image blobs: %w", err)
	}
	for digest, path := range responsePaths {
		paths[digest] = path
	}
	configData, err := os.ReadFile(paths[parsed.Manifest.Config.Digest])
	if err != nil {
		return image.Image{}, fmt.Errorf("read downloaded image config: %w", err)
	}
	parsedConfig, err := imageconfig.Parse(configData, len(parsed.Manifest.Layers))
	if err != nil {
		return image.Image{}, fmt.Errorf("validate image config: %w", err)
	}
	if parsedConfig.OS != resolver.config.Platform.OS || parsedConfig.Architecture != resolver.config.Platform.Architecture {
		return image.Image{}, fmt.Errorf("image config platform %s/%s does not match requested platform %s/%s", parsedConfig.OS, parsedConfig.Architecture, resolver.config.Platform.OS, resolver.config.Platform.Architecture)
	}
	paths, err = resolver.ensureManifestFile(ctx, manifestDescriptor, data, paths)
	if err != nil {
		return image.Image{}, err
	}
	item, err := resolver.installer.ImportDownloaded(ctx, requested, manifestDescriptor, data, paths)
	if err != nil {
		return image.Image{}, fmt.Errorf("install downloaded image into local store: %w", err)
	}
	return item, nil
}

func (resolver *Resolver) getManifest(ctx context.Context, endpoint, scope string) ([]byte, string, string, error) {
	response, err := resolver.client.Get(ctx, endpoint, scope, manifestAccept, 0)
	if err != nil {
		return nil, "", "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, manifest.MaxJSONBlobSize()+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("read manifest response: %w", err)
	}
	if int64(len(data)) > manifest.MaxJSONBlobSize() {
		return nil, "", "", fmt.Errorf("manifest exceeds size limit")
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", "", fmt.Errorf("parse manifest Content-Type: %w", err)
	}
	return data, contentType, response.Header.Get("Docker-Content-Digest"), nil
}

func (resolver *Resolver) cacheManifest(ctx context.Context, descriptor image.Descriptor, data []byte) (string, error) {
	path, err := resolver.cache.Download(ctx, descriptor, func(context.Context, int64) (cache.FetchResponse, error) {
		return cache.FetchResponse{Body: io.NopCloser(bytes.NewReader(data)), StatusCode: http.StatusOK}, nil
	})
	if err != nil {
		return "", fmt.Errorf("cache manifest: %w", err)
	}
	return path, nil
}
func (resolver *Resolver) ensureManifestFile(ctx context.Context, descriptor image.Descriptor, data []byte, paths map[string]string) (map[string]string, error) {
	path, err := resolver.cacheManifest(ctx, descriptor, data)
	if err != nil {
		return nil, err
	}
	paths[descriptor.Digest] = path
	return paths, nil
}
func (resolver *Resolver) existingBlob(descriptor image.Descriptor) (string, bool) {
	path := filepath.Join(resolver.config.ImageRoot, "blobs", "sha256", descriptor.Digest[len("sha256:"):])
	if validator.VerifyFile(path, descriptor) == nil {
		return path, true
	}
	return "", false
}
func normalize(reference image.Reference) (string, string, string) {
	name := reference.Name
	parts := strings.SplitN(name, "/", 2)
	host := "docker.io"
	repo := name
	if len(parts) > 1 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		host, repo = parts[0], parts[1]
	}
	if host == "docker.io" {
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
		host = "registry-1.docker.io"
	}
	if host == "index.docker.io" {
		host = "registry-1.docker.io"
	}
	tag := reference.Tag
	if reference.Digest != "" {
		tag = reference.Digest
	}
	return host, repo, tag
}
func isInsecure(host string, values []string) bool {
	for _, value := range values {
		if host == value {
			return true
		}
	}
	return false
}
func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
