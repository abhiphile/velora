package manifest

import (
	"encoding/json"
	"strings"
	"testing"

	image "github.com/vellora/vellora/internal/image"
)

func TestParseValidManifest(t *testing.T) {
	value := Manifest{SchemaVersion: 2, MediaType: MediaTypeManifest,
		Config: image.Descriptor{MediaType: MediaTypeConfig, Digest: "sha256:" + repeatedDigest('a'), Size: 7},
		Layers: []image.Descriptor{{MediaType: "application/vnd.oci.image.layer.v1.tar", Digest: "sha256:" + repeatedDigest('b'), Size: 0}}}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestParseRejectsBadSchemaMediaAndDescriptor(t *testing.T) {
	valid := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:` + repeatedDigest('a') + `","size":1},"layers":[]}`
	for _, data := range []string{
		strings.ReplaceAll(valid, `"schemaVersion":2`, `"schemaVersion":1`),
		strings.ReplaceAll(valid, `application/vnd.oci.image.manifest.v1+json`, `application/json`),
		strings.ReplaceAll(valid, repeatedDigest('a'), "bad"),
		valid + `{}`,
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("Parse(%q) succeeded", data)
		}
	}
}

func repeatedDigest(value byte) string {
	return strings.Repeat(string(value), 64)
}
