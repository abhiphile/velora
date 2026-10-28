// Package overlay manages per-container OverlayFS layers.
package overlay

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
)

// Config contains host paths used by OverlayFS storage.
type Config struct {
	ImagesRoot  string
	RuntimeRoot string
}

// Paths identifies the lower and per-container OverlayFS directories.
type Paths struct {
	LayerName string
	Lower     string
	Upper     string
	Work      string
	Merged    string
}

// OverlayManager prepares a layer, mounts it in the child namespace, and cleans its owned paths.
type OverlayManager interface {
	Prepare(context.Context, string, string) (Paths, error)
	Mount(context.Context, Paths) error
	Cleanup(context.Context, Paths) error
}

// LayerManager resolves immutable base layers and tracks active references.
type LayerManager interface {
	Resolve(context.Context, string) (string, error)
	Acquire(context.Context, string) error
	Release(context.Context, string) error
}

// MountManager contains Linux mount operations needed by OverlayFS.
type MountManager interface {
	MakeMountsPrivate() error
	MountOverlay(string, string, string, string) error
	Unmount(string) error
}

// Manager coordinates path ownership, base-layer references, and overlay mounts.
type Manager struct {
	config Config
	logger *slog.Logger
	layers LayerManager
	mounts MountManager
	mu     sync.Mutex
	owned  map[string]bool
}

// NewManager creates the platform-specific OverlayFS manager.
func NewManager(config Config, logger *slog.Logger) (OverlayManager, error) {
	if logger == nil {
		return nil, fmt.Errorf("overlay manager requires a logger")
	}
	if config.ImagesRoot == "" || config.RuntimeRoot == "" {
		return nil, fmt.Errorf("overlay image and runtime roots must be configured")
	}
	return newManager(config, logger)
}

func newCore(config Config, logger *slog.Logger, layers LayerManager, mounts MountManager) *Manager {
	return &Manager{config: config, logger: logger, layers: layers, mounts: mounts, owned: make(map[string]bool)}
}

func containerPaths(root string, id string) Paths {
	base := filepath.Join(root, "containers", id)
	return Paths{Upper: filepath.Join(base, "upper"), Work: filepath.Join(base, "work"), Merged: filepath.Join(base, "merged")}
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

func validatePaths(paths Paths) error {
	for label, value := range map[string]string{"lower": paths.Lower, "upper": paths.Upper, "work": paths.Work, "merged": paths.Merged} {
		if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return fmt.Errorf("%s path must be clean and absolute", label)
		}
	}
	return nil
}
