// Package registry assembles the client side of OCI Distribution image pulls.
package registry

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/registry/auth"
	"github.com/vellora/vellora/internal/image/registry/cache"
	"github.com/vellora/vellora/internal/image/registry/client"
	"github.com/vellora/vellora/internal/image/registry/download"
	"github.com/vellora/vellora/internal/image/registry/resolver"
)

type RegistryClient interface {
	Ping(context.Context, string, string) error
	Get(context.Context, string, string, string, int64) (*http.Response, error)
}
type ManifestClient interface {
	FetchManifest(context.Context, string, string, string, string) (io.ReadCloser, string, error)
}
type BlobDownloader interface {
	DownloadMany(context.Context, []image.Descriptor, download.Fetcher) (map[string]string, error)
}
type TokenProvider interface {
	Token(context.Context, auth.Challenge) (string, error)
}
type Cache interface {
	Path(string) (string, error)
	Verified(image.Descriptor) (string, bool)
	Download(context.Context, image.Descriptor, cache.Fetcher) (string, error)
}
type ImageResolver interface {
	Pull(context.Context, string) (image.Image, error)
}

type Config = resolver.Config

func New(config Config, local image.ImageResolver, installer image.DownloadedImageStore, httpClient *http.Client, logger *slog.Logger) (*resolver.Resolver, error) {
	return resolver.New(config, local, installer, httpClient, logger)
}

var _ RegistryClient = (*client.HTTP)(nil)
var _ ManifestClient = (*client.HTTP)(nil)
