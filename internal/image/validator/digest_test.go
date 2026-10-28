package validator

import (
	"bytes"
	"strings"
	"testing"

	image "github.com/vellora/vellora/internal/image"
)

func TestVerifyReaderChecksSHA256AndSize(t *testing.T) {
	descriptor := image.Descriptor{MediaType: "application/test", Digest: "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", Size: 3}
	if err := VerifyReader(strings.NewReader("abc"), descriptor); err != nil {
		t.Fatalf("VerifyReader: %v", err)
	}
	if err := VerifyReader(strings.NewReader("abd"), descriptor); err == nil {
		t.Fatal("accepted digest mismatch")
	}
	descriptor.Size = 2
	if err := VerifyReader(strings.NewReader("abc"), descriptor); err == nil {
		t.Fatal("accepted size mismatch")
	}
}

func TestCopyVerifiedStreamsVerifiedContent(t *testing.T) {
	descriptor := image.Descriptor{MediaType: "application/test", Digest: "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", Size: 3}
	var output bytes.Buffer
	if err := CopyVerified(&output, strings.NewReader("abc"), descriptor); err != nil {
		t.Fatalf("CopyVerified: %v", err)
	}
	if output.String() != "abc" {
		t.Fatalf("copied %q, want abc", output.String())
	}
}
