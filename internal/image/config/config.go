// Package config parses the execution configuration blob of an OCI image.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/vellora/vellora/internal/image/validator"
)

const MediaType = "application/vnd.oci.image.config.v1+json"

// Runtime contains image execution defaults from the OCI config object.
type Runtime struct {
	User       string            `json:"User,omitempty"`
	Env        []string          `json:"Env,omitempty"`
	Entrypoint []string          `json:"Entrypoint,omitempty"`
	Cmd        []string          `json:"Cmd,omitempty"`
	WorkingDir string            `json:"WorkingDir,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
}

// RootFS identifies the ordered uncompressed layer digests described by an image config.
type RootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

// History describes image build history; empty_layer entries need not have a layer blob.
type History struct {
	CreatedBy  string `json:"created_by,omitempty"`
	Comment    string `json:"comment,omitempty"`
	EmptyLayer bool   `json:"empty_layer,omitempty"`
}

// ImageConfig is the OCI image config blob.
type ImageConfig struct {
	Architecture string    `json:"architecture"`
	OS           string    `json:"os"`
	Variant      string    `json:"variant,omitempty"`
	Created      string    `json:"created,omitempty"`
	Config       Runtime   `json:"config,omitempty"`
	RootFS       RootFS    `json:"rootfs"`
	History      []History `json:"history,omitempty"`
}

// Parse decodes and validates an OCI image config against its manifest's layer count.
func Parse(data []byte, layerCount int) (ImageConfig, error) {
	var parsed ImageConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&parsed); err != nil {
		return ImageConfig{}, fmt.Errorf("decode OCI image config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ImageConfig{}, fmt.Errorf("OCI image config contains trailing JSON values")
		}
		return ImageConfig{}, fmt.Errorf("decode trailing OCI image config data: %w", err)
	}
	if strings.TrimSpace(parsed.OS) == "" {
		return ImageConfig{}, fmt.Errorf("image config os is required")
	}
	if strings.TrimSpace(parsed.Architecture) == "" {
		return ImageConfig{}, fmt.Errorf("image config architecture is required")
	}
	if parsed.RootFS.Type != "layers" {
		return ImageConfig{}, fmt.Errorf("image config rootfs.type must be %q, got %q", "layers", parsed.RootFS.Type)
	}
	if parsed.RootFS.DiffIDs == nil {
		return ImageConfig{}, fmt.Errorf("image config rootfs.diff_ids array is required")
	}
	if len(parsed.RootFS.DiffIDs) != layerCount {
		return ImageConfig{}, fmt.Errorf("image config has %d diff_ids but manifest has %d layers", len(parsed.RootFS.DiffIDs), layerCount)
	}
	for index, digest := range parsed.RootFS.DiffIDs {
		if err := validator.ValidateDigest(digest); err != nil {
			return ImageConfig{}, fmt.Errorf("invalid rootfs diff_id %d: %w", index, err)
		}
	}
	for _, environment := range parsed.Config.Env {
		if !strings.Contains(environment, "=") {
			return ImageConfig{}, fmt.Errorf("invalid image environment entry %q", environment)
		}
	}
	return parsed, nil
}
