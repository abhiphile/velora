package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	image "github.com/vellora/vellora/internal/image"
	localmanifest "github.com/vellora/vellora/internal/image/manifest"
)

func TestSelectHostPlatformFromOCIIndex(t *testing.T) {
	linux := image.Descriptor{MediaType: localmanifest.MediaTypeManifest, Digest: "sha256:" + repeat("a", 64), Size: 123, Platform: &image.Platform{OS: "linux", Architecture: "amd64"}}
	other := image.Descriptor{MediaType: localmanifest.MediaTypeManifest, Digest: "sha256:" + repeat("b", 64), Size: 222, Platform: &image.Platform{OS: "linux", Architecture: "arm64"}}
	data, err := json.Marshal(Index{SchemaVersion: 2, MediaType: localmanifest.MediaTypeIndex, Manifests: []image.Descriptor{other, linux}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := ParseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Select(index, image.Platform{OS: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Digest != linux.Digest {
		t.Fatalf("selected=%s", selected.Digest)
	}
}

func TestParseDockerSchema2Manifest(t *testing.T) {
	configBytes := []byte(`{"architecture":"amd64"}`)
	sum := sha256.Sum256(configBytes)
	value := localmanifest.Manifest{SchemaVersion: 2, MediaType: localmanifest.DockerManifest, Config: image.Descriptor{MediaType: localmanifest.DockerConfig, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(configBytes))}, Layers: []image.Descriptor{}}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data, localmanifest.DockerManifest)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Manifest.MediaType != localmanifest.DockerManifest {
		t.Fatalf("mediaType=%q", parsed.Manifest.MediaType)
	}
}

func TestSelectArm64DefaultsToV8WhenVariantIsUnspecified(t *testing.T) {
	index := Index{SchemaVersion: 2, Manifests: []image.Descriptor{
		{MediaType: localmanifest.MediaTypeManifest, Digest: "sha256:" + repeat("a", 64), Size: 10, Platform: &image.Platform{OS: "linux", Architecture: "arm64", Variant: "v9"}},
		{MediaType: localmanifest.MediaTypeManifest, Digest: "sha256:" + repeat("b", 64), Size: 11, Platform: &image.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}},
	}}
	selected, err := Select(index, image.Platform{OS: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Platform.Variant != "v8" {
		t.Fatalf("selected variant=%q", selected.Platform.Variant)
	}
}
func repeat(value string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += value
	}
	return result
}
