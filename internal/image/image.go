// Package image defines OCI image domain models and subsystem interfaces.
package image

import (
	"context"
	"io"
)

// Reference is a local image name with an optional tag and content digest.
type Reference struct {
	Name   string
	Tag    string
	Digest string
}

// String formats the reference in its canonical human-readable form.
func (reference Reference) String() string {
	value := reference.Name
	if reference.Tag != "" {
		value += ":" + reference.Tag
	}
	if reference.Digest != "" {
		value += "@" + reference.Digest
	}
	return value
}

// Platform identifies an image's target operating system and architecture.
type Platform struct {
	OS           string `json:"os,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Variant      string `json:"variant,omitempty"`
}

// Descriptor identifies a content-addressed OCI object.
type Descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Platform    *Platform         `json:"platform,omitempty"`
}

// Layer is an ordered filesystem layer referenced by an image manifest.
type Layer struct {
	Digest    string `json:"digest"`
	DiffID    string `json:"diff_id,omitempty"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
}

// Image is the validated metadata needed to identify and inspect a local OCI image.
type Image struct {
	Reference        string            `json:"reference"`
	ManifestDigest   string            `json:"manifest_digest"`
	ConfigDigest     string            `json:"config_digest"`
	Architecture     string            `json:"architecture"`
	OS               string            `json:"os"`
	Variant          string            `json:"variant,omitempty"`
	Entrypoint       []string          `json:"entrypoint,omitempty"`
	Command          []string          `json:"command,omitempty"`
	Environment      []string          `json:"environment,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	User             string            `json:"user,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	Layers           []Layer           `json:"layers"`
}

// ImageStore imports, resolves, lists, and removes local OCI images.
type ImageStore interface {
	Create(context.Context, Reference, string) (Image, error)
	Load(context.Context, Reference) (Image, error)
	Delete(context.Context, Reference) error
	List(context.Context) ([]Image, error)
}

// DownloadedImageStore installs already-fetched registry content into a local store.
type DownloadedImageStore interface {
	ImportDownloaded(context.Context, Reference, Descriptor, []byte, map[string]string) (Image, error)
}

// ImageResolver resolves a tag or digest to validated image metadata.
type ImageResolver interface {
	Resolve(context.Context, Reference) (Image, error)
}

// LayerStore opens verified layer blobs by digest.
type LayerStore interface {
	Open(context.Context, string) (io.ReadCloser, error)
}

// ManifestLoader reads a descriptor-addressed JSON object from a local layout.
type ManifestLoader interface {
	Load(context.Context, string, Descriptor) ([]byte, error)
}

// ReferenceParser parses and validates user-supplied image references.
type ReferenceParser interface {
	Parse(string) (Reference, error)
}
