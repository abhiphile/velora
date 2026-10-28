// Package validator verifies OCI descriptors and content-addressed blobs.
package validator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	image "github.com/vellora/vellora/internal/image"
)

// ValidateDigest accepts the SHA-256 digest algorithm supported by Vellora.
func ValidateDigest(value string) error {
	if !strings.HasPrefix(value, "sha256:") {
		return fmt.Errorf("unsupported digest %q: only sha256 is supported", value)
	}
	encoded := strings.TrimPrefix(value, "sha256:")
	if len(encoded) != sha256.Size*2 {
		return fmt.Errorf("invalid sha256 digest length %d", len(encoded))
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("invalid sha256 digest encoding %q", value)
	}
	if strings.ToLower(encoded) != encoded {
		return fmt.Errorf("sha256 digest must use lowercase hexadecimal")
	}
	return nil
}

// ValidateDescriptor checks the fields required before a descriptor is used as a path or size bound.
func ValidateDescriptor(descriptor image.Descriptor) error {
	if descriptor.MediaType == "" {
		return fmt.Errorf("descriptor mediaType is required")
	}
	if descriptor.Size < 0 {
		return fmt.Errorf("descriptor size cannot be negative")
	}
	if err := ValidateDigest(descriptor.Digest); err != nil {
		return err
	}
	return nil
}

// VerifyReader hashes a stream and verifies its exact size and digest.
func VerifyReader(reader io.Reader, expected image.Descriptor) error {
	if err := ValidateDescriptor(expected); err != nil {
		return fmt.Errorf("validate descriptor: %w", err)
	}
	hasher := sha256.New()
	size, err := io.Copy(hasher, io.LimitReader(reader, verificationLimit(expected.Size)))
	if err != nil {
		return fmt.Errorf("hash blob: %w", err)
	}
	if size != expected.Size {
		return fmt.Errorf("blob size mismatch: expected %d bytes, got %d", expected.Size, size)
	}
	actual := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if actual != expected.Digest {
		return fmt.Errorf("blob digest mismatch: expected %s, got %s", expected.Digest, actual)
	}
	return nil
}

// VerifyFile verifies a file against an OCI descriptor without loading it all into memory.
func VerifyFile(path string, expected image.Descriptor) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open blob %q: %w", path, err)
	}
	defer file.Close()
	return VerifyReader(file, expected)
}

// CopyVerified copies a blob into a writer while verifying the descriptor.
func CopyVerified(destination io.Writer, source io.Reader, expected image.Descriptor) error {
	if err := ValidateDescriptor(expected); err != nil {
		return fmt.Errorf("validate descriptor: %w", err)
	}
	hasher := sha256.New()
	writer := io.MultiWriter(destination, hasher)
	size, err := io.Copy(writer, io.LimitReader(source, verificationLimit(expected.Size)))
	if err != nil {
		return fmt.Errorf("copy blob: %w", err)
	}
	if size != expected.Size {
		return fmt.Errorf("blob size mismatch: expected %d bytes, got %d", expected.Size, size)
	}
	actual := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if actual != expected.Digest {
		return fmt.Errorf("blob digest mismatch: expected %s, got %s", expected.Digest, actual)
	}
	return nil
}

func verificationLimit(size int64) int64 {
	if size == 1<<63-1 {
		return size
	}
	return size + 1
}
