package resolver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	image "github.com/vellora/vellora/internal/image"
	localManifest "github.com/vellora/vellora/internal/image/manifest"
	"github.com/vellora/vellora/internal/image/reference"
	"github.com/vellora/vellora/internal/image/registry/cache"
	"github.com/vellora/vellora/internal/image/registry/download"
	registryManifest "github.com/vellora/vellora/internal/image/registry/manifest"
	"github.com/vellora/vellora/internal/image/store"
	"github.com/vellora/vellora/internal/image/testutil"
)

type mockRegistry struct {
	fixture         testutil.Fixture
	indexData       []byte
	indexDescriptor image.Descriptor
	requests        atomic.Int64
}

func (mock *mockRegistry) Ping(context.Context, string, string) error { return nil }
func (mock *mockRegistry) Get(_ context.Context, endpoint, _, _ string, offset int64) (*http.Response, error) {
	mock.requests.Add(1)
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	var descriptor image.Descriptor
	var data []byte
	if strings.HasSuffix(requestURL.Path, "/manifests/latest") {
		if mock.indexData != nil {
			descriptor = mock.indexDescriptor
			data = mock.indexData
		} else {
			descriptor = mock.fixture.Manifest
		}
	} else if mock.indexData != nil && strings.HasSuffix(requestURL.Path, "/manifests/"+mock.indexDescriptor.Digest) {
		descriptor = mock.indexDescriptor
		data = mock.indexData
	} else if strings.Contains(requestURL.Path, "/manifests/") && strings.HasSuffix(requestURL.Path, mock.fixture.Manifest.Digest) {
		descriptor = mock.fixture.Manifest
	} else if marker := strings.LastIndex(requestURL.Path, "/blobs/"); marker >= 0 {
		digest, err := url.PathUnescape(requestURL.Path[marker+len("/blobs/"):])
		if err != nil {
			return nil, err
		}
		for _, candidate := range []image.Descriptor{mock.fixture.Config, mock.fixture.Layer} {
			if candidate.Digest == digest {
				descriptor = candidate
				break
			}
		}
	}
	if descriptor.Digest == "" {
		return nil, os.ErrNotExist
	}
	path := filepath.Join(mock.fixture.Root, "blobs", "sha256", strings.TrimPrefix(descriptor.Digest, "sha256:"))
	if data == nil {
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}
	headers := make(http.Header)
	headers.Set("Content-Type", descriptor.MediaType)
	if strings.Contains(requestURL.Path, "/manifests/") {
		headers.Set("Docker-Content-Digest", descriptor.Digest)
	}
	status := 200
	body := io.NopCloser(strings.NewReader(string(data)))
	if offset > 0 {
		if offset >= int64(len(data)) {
			return nil, os.ErrNotExist
		}
		body = io.NopCloser(strings.NewReader(string(data[offset:])))
		status = 206
		headers.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(data)-1, len(data)))
	}
	return &http.Response{StatusCode: status, Status: "200 OK", Header: headers, Body: body}, nil
}

func TestPullDigestPinnedMultiArchitectureIndex(t *testing.T) {
	root := t.TempDir()
	fixture := testutil.Create(t, filepath.Join(root, "fixture"))
	indexValue := registryManifest.Index{SchemaVersion: 2, MediaType: localManifest.MediaTypeIndex, Manifests: []image.Descriptor{{MediaType: fixture.Manifest.MediaType, Digest: fixture.Manifest.Digest, Size: fixture.Manifest.Size, Platform: &image.Platform{OS: "linux", Architecture: "amd64"}}}}
	indexData, err := json.Marshal(indexValue)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(indexData)
	indexDescriptor := image.Descriptor{MediaType: localManifest.MediaTypeIndex, Digest: "sha256:" + hex.EncodeToString(digest[:]), Size: int64(len(indexData))}
	imageRoot := filepath.Join(root, "images")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	local, err := store.NewLocal(imageRoot, logger)
	if err != nil {
		t.Fatal(err)
	}
	blobCache, err := cache.New(filepath.Join(imageRoot, "cache", "registry"))
	if err != nil {
		t.Fatal(err)
	}
	downloader, err := download.New(blobCache, 2, logger)
	if err != nil {
		t.Fatal(err)
	}
	remote := &mockRegistry{fixture: fixture, indexData: indexData, indexDescriptor: indexDescriptor}
	puller := newResolver(Config{ImageRoot: imageRoot, Platform: image.Platform{OS: "linux", Architecture: "amd64"}}, local, local, remote, downloader, blobCache, logger)
	requested := "registry.test/alpine@" + indexDescriptor.Digest
	pulled, err := puller.Pull(context.Background(), requested)
	if err != nil {
		t.Fatal(err)
	}
	if pulled.ManifestDigest != fixture.Manifest.Digest {
		t.Fatalf("platform manifest=%s", pulled.ManifestDigest)
	}
	parsed, err := reference.NewParser().Parse(requested)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := local.Resolve(context.Background(), parsed)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ManifestDigest != fixture.Manifest.Digest {
		t.Fatalf("digest alias resolved to %s", resolved.ManifestDigest)
	}
}

func TestPullImportsAndReusesLocalImage(t *testing.T) {
	root := t.TempDir()
	fixture := testutil.Create(t, filepath.Join(root, "fixture"))
	imageRoot := filepath.Join(root, "images")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	local, err := store.NewLocal(imageRoot, logger)
	if err != nil {
		t.Fatal(err)
	}
	blobCache, err := cache.New(filepath.Join(imageRoot, "cache", "registry"))
	if err != nil {
		t.Fatal(err)
	}
	downloader, err := download.New(blobCache, 2, logger)
	if err != nil {
		t.Fatal(err)
	}
	registry := &mockRegistry{fixture: fixture}
	puller := newResolver(Config{ImageRoot: imageRoot, Platform: image.Platform{OS: "linux", Architecture: "amd64"}}, local, local, registry, downloader, blobCache, logger)
	first, err := puller.Pull(context.Background(), "registry.test/alpine:latest")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Layers) != 1 || first.ManifestDigest != fixture.Manifest.Digest {
		t.Fatalf("image metadata=%+v", first)
	}
	afterFirst := registry.requests.Load()
	second, err := puller.Pull(context.Background(), "registry.test/alpine:latest")
	if err != nil {
		t.Fatal(err)
	}
	if second.ManifestDigest != first.ManifestDigest || registry.requests.Load() != afterFirst {
		t.Fatal("second pull did not reuse local image")
	}
	if _, err := os.Stat(filepath.Join(imageRoot, "blobs", "sha256", strings.TrimPrefix(fixture.Layer.Digest, "sha256:"))); err != nil {
		t.Fatalf("layer was not installed: %v", err)
	}
}
