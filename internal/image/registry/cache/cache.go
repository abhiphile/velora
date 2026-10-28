// Package cache stores verified downloads by immutable content digest.
package cache

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/validator"
	"golang.org/x/sys/unix"
)

type FetchResponse struct {
	Body         io.ReadCloser
	StatusCode   int
	ContentRange string
}
type Fetcher func(context.Context, int64) (FetchResponse, error)
type Cache interface {
	Path(string) (string, error)
	Verified(image.Descriptor) (string, bool)
	Download(context.Context, image.Descriptor, Fetcher) (string, error)
}

type Filesystem struct{ root string }

func New(root string) (*Filesystem, error) {
	if root == "" {
		return nil, fmt.Errorf("blob cache root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve cache root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(abs, "sha256"), 0755); err != nil {
		return nil, fmt.Errorf("create cache directory: %w", err)
	}
	return &Filesystem{root: abs}, nil
}

func (cache *Filesystem) Path(digest string) (string, error) {
	if err := validator.ValidateDigest(digest); err != nil {
		return "", fmt.Errorf("cache digest: %w", err)
	}
	return filepath.Join(cache.root, "sha256", digest[len("sha256:"):]), nil
}

func (cache *Filesystem) Verified(descriptor image.Descriptor) (string, bool) {
	path, err := cache.Path(descriptor.Digest)
	if err != nil {
		return "", false
	}
	if err := validator.VerifyFile(path, descriptor); err != nil {
		_ = os.Remove(path)
		return path, false
	}
	return path, true
}

// Download resumes a partial response with Range, but only publishes a complete verified file.
func (cache *Filesystem) Download(ctx context.Context, descriptor image.Descriptor, fetch Fetcher) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("download blob: %w", err)
	}
	path, err := cache.Path(descriptor.Digest)
	if err != nil {
		return "", err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", fmt.Errorf("open cache lock: %w", err)
	}
	defer lock.Close()
	for {
		err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return "", fmt.Errorf("lock cached blob: %w", err)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", fmt.Errorf("wait for cached blob download: %w", ctx.Err())
		case <-timer.C:
		}
	}
	if existing, ok := cache.Verified(descriptor); ok {
		return existing, nil
	}
	partial := path + ".partial"
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", fmt.Errorf("open partial download: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return "", fmt.Errorf("inspect partial download: %w", err)
	}
	offset := info.Size()
	if offset > descriptor.Size {
		file.Close()
		_ = os.Remove(partial)
		offset = 0
		file, err = os.OpenFile(partial, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
		if err != nil {
			return "", fmt.Errorf("reset partial download: %w", err)
		}
	}
	if offset == descriptor.Size {
		_ = file.Close()
		if err := validator.VerifyFile(partial, descriptor); err == nil {
			if err := os.Rename(partial, path); err != nil {
				return "", fmt.Errorf("publish completed partial download: %w", err)
			}
			return path, nil
		}
		_ = os.Remove(partial)
		file, err = os.OpenFile(partial, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
		if err != nil {
			return "", fmt.Errorf("reset invalid completed partial: %w", err)
		}
		offset = 0
	}
	response, err := fetch(ctx, offset)
	if err != nil {
		file.Close()
		return "", fmt.Errorf("fetch blob at byte %d: %w", offset, err)
	}
	if response.Body == nil {
		file.Close()
		return "", fmt.Errorf("registry returned an empty blob response")
	}
	defer response.Body.Close()
	if offset > 0 && response.StatusCode == 200 {
		if err := file.Truncate(0); err != nil {
			file.Close()
			return "", fmt.Errorf("restart partial download: %w", err)
		}
		offset = 0
	} else if offset > 0 {
		start, total, valid := parseContentRange(response.ContentRange)
		if response.StatusCode != 206 || !valid || start != offset || total != descriptor.Size {
			file.Close()
			return "", fmt.Errorf("invalid range response %q for offset %d", response.ContentRange, offset)
		}
	} else if response.StatusCode != 200 {
		file.Close()
		return "", fmt.Errorf("blob request returned HTTP status %d", response.StatusCode)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		file.Close()
		return "", fmt.Errorf("seek partial download: %w", err)
	}
	written, copyErr := io.Copy(file, contextReader{ctx: ctx, reader: io.LimitReader(response.Body, descriptor.Size-offset+1)})
	if copyErr != nil {
		_ = file.Sync()
		file.Close()
		return "", fmt.Errorf("write partial download: %w", copyErr)
	}
	if offset+written > descriptor.Size {
		file.Close()
		_ = os.Remove(partial)
		return "", fmt.Errorf("download exceeds expected size %d", descriptor.Size)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", fmt.Errorf("sync partial download: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close partial download: %w", err)
	}
	if offset+written != descriptor.Size {
		return "", fmt.Errorf("partial download has %d of %d bytes", offset+written, descriptor.Size)
	}
	if err := validator.VerifyFile(partial, descriptor); err != nil {
		_ = os.Remove(partial)
		return "", fmt.Errorf("verify downloaded content: %w", err)
	}
	if err := os.Rename(partial, path); err != nil {
		return "", fmt.Errorf("publish cached blob: %w", err)
	}
	return path, nil
}

func parseContentRange(value string) (int64, int64, bool) {
	var start, end, total int64
	if _, err := fmt.Sscanf(value, "bytes %d-%d/%d", &start, &end, &total); err != nil {
		return 0, 0, false
	}
	if start < 0 || end < start || total <= end {
		return 0, 0, false
	}
	return start, total, true
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

var _ Cache = (*Filesystem)(nil)
