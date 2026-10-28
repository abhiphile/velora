// Package manifest parses OCI and Docker schema 2 registry manifests.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	image "github.com/vellora/vellora/internal/image"
	localmanifest "github.com/vellora/vellora/internal/image/manifest"
	"github.com/vellora/vellora/internal/image/registry/validator"
)

type Index struct {
	SchemaVersion int                `json:"schemaVersion"`
	MediaType     string             `json:"mediaType"`
	Manifests     []image.Descriptor `json:"manifests"`
}
type Parsed struct {
	Manifest   localmanifest.Manifest
	Descriptor image.Descriptor
}

func ParseIndex(data []byte) (Index, error) {
	var value Index
	if err := json.Unmarshal(data, &value); err != nil {
		return Index{}, fmt.Errorf("decode image index: %w", err)
	}
	if value.SchemaVersion != 2 {
		return Index{}, fmt.Errorf("image index schemaVersion must be 2")
	}
	if value.MediaType != "" && value.MediaType != localmanifest.MediaTypeIndex && value.MediaType != localmanifest.DockerManifestList {
		return Index{}, fmt.Errorf("unsupported image index media type %q", value.MediaType)
	}
	if value.Manifests == nil {
		return Index{}, fmt.Errorf("image index manifests is required")
	}
	for index, descriptor := range value.Manifests {
		if err := validator.ValidateDescriptor(descriptor); err != nil {
			return Index{}, fmt.Errorf("invalid image index descriptor %d: %w", index, err)
		}
	}
	return value, nil
}

func Select(index Index, platform image.Platform) (image.Descriptor, error) {
	var matches []image.Descriptor
	for _, descriptor := range index.Manifests {
		if descriptor.MediaType != localmanifest.MediaTypeManifest && descriptor.MediaType != localmanifest.DockerManifest {
			continue
		}
		if descriptor.Platform == nil {
			continue
		}
		p := descriptor.Platform
		if p.OS == platform.OS && p.Architecture == platform.Architecture && (platform.Variant == "" || p.Variant == platform.Variant) {
			matches = append(matches, descriptor)
		}
	}
	if len(matches) == 0 {
		return image.Descriptor{}, fmt.Errorf("image index has no %s/%s platform", platform.OS, platform.Architecture)
	}
	if platform.Variant == "" && len(matches) > 1 {
		var unvarianted []image.Descriptor
		for _, descriptor := range matches {
			if descriptor.Platform.Variant == "" {
				unvarianted = append(unvarianted, descriptor)
			}
		}
		if len(unvarianted) == 1 {
			return unvarianted[0], nil
		}
		if platform.Architecture == "arm64" {
			for _, descriptor := range matches {
				if descriptor.Platform.Variant == "v8" {
					return descriptor, nil
				}
			}
		}
		if len(unvarianted) > 1 {
			matches = unvarianted
		}
	}
	if len(matches) > 1 {
		return image.Descriptor{}, fmt.Errorf("image index has ambiguous %s/%s platform; specify --platform with a variant", platform.OS, platform.Architecture)
	}
	return matches[0], nil
}

func Parse(data []byte, mediaType string) (Parsed, error) {
	parsed, err := localmanifest.Parse(data)
	if err != nil {
		return Parsed{}, err
	}
	actual := parsed.MediaType
	if mediaType != "" && strings.Contains(mediaType, ";") {
		mediaType = strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0])
	}
	if mediaType != "" && mediaType != actual {
		return Parsed{}, fmt.Errorf("manifest Content-Type %q disagrees with body %q", mediaType, actual)
	}
	sum := sha256.Sum256(data)
	descriptor := image.Descriptor{MediaType: actual, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(data))}
	return Parsed{Manifest: parsed, Descriptor: descriptor}, nil
}
