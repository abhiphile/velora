package download

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/registry/cache"
)

func TestDownloadManyDeduplicatesAndBoundsConcurrency(t *testing.T) {
	blobCache, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(blobCache, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	values := []string{"first-blob", "second-blob"}
	descriptors := make([]image.Descriptor, 0, 3)
	for _, value := range values {
		sum := sha256.Sum256([]byte(value))
		descriptors = append(descriptors, image.Descriptor{MediaType: "application/octet-stream", Digest: fmt.Sprintf("sha256:%x", sum), Size: int64(len(value))})
	}
	descriptors = append(descriptors, descriptors[0])
	var active, peak, calls atomic.Int64
	paths, err := manager.DownloadMany(context.Background(), descriptors, func(_ context.Context, descriptor image.Descriptor, _ int64) (cache.FetchResponse, error) {
		calls.Add(1)
		current := active.Add(1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		active.Add(-1)
		value := values[0]
		if descriptor.Digest == descriptors[1].Digest {
			value = values[1]
		}
		return cache.FetchResponse{Body: io.NopCloser(strings.NewReader(value)), StatusCode: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || calls.Load() != 2 {
		t.Fatalf("paths=%d fetch calls=%d", len(paths), calls.Load())
	}
	if peak.Load() > 2 || peak.Load() < 2 {
		t.Fatalf("peak concurrency=%d", peak.Load())
	}
}
