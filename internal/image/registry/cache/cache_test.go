package cache

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"

	image "github.com/vellora/vellora/internal/image"
)

func TestDownloadResumesPartialBlob(t *testing.T) {
	data := []byte("verified-layer-content")
	sum := sha256.Sum256(data)
	descriptor := image.Descriptor{MediaType: "application/octet-stream", Digest: fmt.Sprintf("sha256:%x", sum), Size: int64(len(data))}
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	fetch := func(_ context.Context, offset int64) (FetchResponse, error) {
		calls++
		if calls == 1 {
			return FetchResponse{Body: io.NopCloser(strings.NewReader(string(data[:7]))), StatusCode: 200}, nil
		}
		return FetchResponse{Body: io.NopCloser(strings.NewReader(string(data[offset:]))), StatusCode: 206, ContentRange: fmt.Sprintf("bytes %d-%d/%d", offset, len(data)-1, len(data))}, nil
	}
	if _, err := store.Download(context.Background(), descriptor, fetch); err == nil {
		t.Fatal("short response was accepted")
	}
	path, err := store.Download(context.Background(), descriptor, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("fetch calls=%d", calls)
	}
	if got, ok := store.Verified(descriptor); !ok || got != path {
		t.Fatalf("cache path=%q verified=%v", got, ok)
	}
}

func TestDownloadRejectsDigestMismatch(t *testing.T) {
	data := []byte("expected")
	sum := sha256.Sum256(data)
	descriptor := image.Descriptor{MediaType: "application/octet-stream", Digest: fmt.Sprintf("sha256:%x", sum), Size: int64(len(data))}
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Download(context.Background(), descriptor, func(context.Context, int64) (FetchResponse, error) {
		return FetchResponse{Body: io.NopCloser(strings.NewReader("corrupt!")), StatusCode: 200}, nil
	})
	if err == nil {
		t.Fatal("accepted a digest mismatch")
	}
	if _, ok := store.Verified(descriptor); ok {
		t.Fatal("published corrupt content to cache")
	}
}
