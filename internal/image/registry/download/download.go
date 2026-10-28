// Package download coordinates verified, bounded, resumable blob downloads.
package download

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/registry/cache"
	"github.com/vellora/vellora/internal/image/registry/validator"
)

type Fetcher func(context.Context, image.Descriptor, int64) (cache.FetchResponse, error)
type BlobDownloader interface {
	DownloadMany(context.Context, []image.Descriptor, Fetcher) (map[string]string, error)
}
type flight struct {
	done chan struct{}
	path string
	err  error
}
type Manager struct {
	cache    cache.Cache
	logger   *slog.Logger
	parallel int
	mu       sync.Mutex
	flights  map[string]*flight
}

func New(cache cache.Cache, parallel int, logger *slog.Logger) (*Manager, error) {
	if cache == nil || logger == nil {
		return nil, fmt.Errorf("blob downloader requires cache and logger")
	}
	if parallel < 1 {
		parallel = 1
	}
	if parallel > 16 {
		parallel = 16
	}
	return &Manager{cache: cache, logger: logger, parallel: parallel, flights: make(map[string]*flight)}, nil
}

func (manager *Manager) DownloadMany(ctx context.Context, descriptors []image.Descriptor, fetcher Fetcher) (map[string]string, error) {
	unique := make([]image.Descriptor, 0, len(descriptors))
	seen := make(map[string]image.Descriptor)
	for _, descriptor := range descriptors {
		if err := validator.ValidateDescriptor(descriptor); err != nil {
			return nil, fmt.Errorf("validate blob descriptor: %w", err)
		}
		if previous, ok := seen[descriptor.Digest]; ok {
			if previous.Size != descriptor.Size {
				return nil, fmt.Errorf("digest %s has conflicting sizes", descriptor.Digest)
			}
			continue
		}
		seen[descriptor.Digest] = descriptor
		unique = append(unique, descriptor)
	}
	if fetcher == nil {
		return nil, fmt.Errorf("blob fetcher is required")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		digest, path string
		err          error
	}
	jobs := make(chan image.Descriptor)
	results := make(chan result, len(unique))
	workers := manager.parallel
	if workers > len(unique) {
		workers = len(unique)
	}
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for descriptor := range jobs {
				path, err := manager.downloadOne(ctx, descriptor, fetcher)
				results <- result{digest: descriptor.Digest, path: path, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, descriptor := range unique {
			select {
			case <-ctx.Done():
				return
			case jobs <- descriptor:
			}
		}
	}()
	go func() { group.Wait(); close(results) }()
	paths := make(map[string]string, len(unique))
	var firstErr error
	for result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		if result.err == nil {
			paths[result.digest] = result.path
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("download blobs: %w", err)
	}
	return paths, nil
}

func (manager *Manager) downloadOne(ctx context.Context, descriptor image.Descriptor, fetcher Fetcher) (string, error) {
	if path, ok := manager.cache.Verified(descriptor); ok {
		manager.logger.Info("registry blob cache hit", "digest", descriptor.Digest, "path", path)
		return path, nil
	}
	manager.logger.Info("registry blob cache miss", "digest", descriptor.Digest, "size", descriptor.Size)
	manager.mu.Lock()
	if existing := manager.flights[descriptor.Digest]; existing != nil {
		manager.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-existing.done:
			return existing.path, existing.err
		}
	}
	current := &flight{done: make(chan struct{})}
	manager.flights[descriptor.Digest] = current
	manager.mu.Unlock()
	defer func() {
		manager.mu.Lock()
		delete(manager.flights, descriptor.Digest)
		close(current.done)
		manager.mu.Unlock()
	}()
	if path, ok := manager.cache.Verified(descriptor); ok {
		current.path = path
		return path, nil
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		current.path, err = manager.cache.Download(ctx, descriptor, func(callCtx context.Context, offset int64) (cache.FetchResponse, error) {
			return fetcher(callCtx, descriptor, offset)
		})
		if err == nil {
			manager.logger.Info("downloaded and verified blob", "digest", descriptor.Digest, "size", descriptor.Size)
			return current.path, nil
		}
		if !retryable(err) || attempt == 2 {
			break
		}
		manager.logger.Warn("retrying blob download", "digest", descriptor.Digest, "attempt", attempt+1, "error", err)
		timer := time.NewTimer(time.Duration(1<<attempt) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			current.err = fmt.Errorf("blob download canceled: %w", ctx.Err())
			return "", current.err
		case <-timer.C:
		}
	}
	current.err = fmt.Errorf("download blob %s: %w", descriptor.Digest, err)
	return "", current.err
}

func retryable(err error) bool {
	message := err.Error()
	if strings.Contains(message, "registry returned HTTP ") || strings.Contains(message, "blob request returned HTTP status") {
		return false
	}
	return !strings.Contains(message, "digest mismatch") && !strings.Contains(message, "size mismatch") && !strings.Contains(message, "exceeds expected size") && !strings.Contains(message, "invalid range response") && !strings.Contains(message, "unsupported digest")
}

var _ BlobDownloader = (*Manager)(nil)
