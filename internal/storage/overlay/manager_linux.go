//go:build linux

package overlay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

type directoryLayers struct {
	root       string
	mu         sync.Mutex
	references map[string]int
}
type unixMounts struct{}

func newManager(config Config, logger *slog.Logger) (OverlayManager, error) {
	images, err := filepath.Abs(config.ImagesRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve images root: %w", err)
	}
	runtimeRoot, err := filepath.Abs(config.RuntimeRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime root: %w", err)
	}
	return newCore(Config{ImagesRoot: images, RuntimeRoot: runtimeRoot}, logger, &directoryLayers{root: filepath.Join(images, "layers"), references: make(map[string]int)}, unixMounts{}), nil
}

func (manager *Manager) Prepare(ctx context.Context, id, layerName string) (Paths, error) {
	if err := ctx.Err(); err != nil {
		return Paths{}, fmt.Errorf("prepare overlay: %w", err)
	}
	if !validName(layerName) || !validName(id) {
		return Paths{}, fmt.Errorf("container ID and layer name must be simple path components")
	}
	lower, err := manager.layers.Resolve(ctx, layerName)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve lower layer %q: %w", layerName, err)
	}
	if err := manager.layers.Acquire(ctx, lower); err != nil {
		return Paths{}, fmt.Errorf("acquire lower layer %q: %w", lower, err)
	}
	paths := containerPaths(manager.config.RuntimeRoot, id)
	paths.LayerName, paths.Lower = layerName, lower
	base := filepath.Dir(paths.Upper)
	if err := os.MkdirAll(filepath.Dir(base), 0700); err != nil {
		return Paths{}, manager.releasePrepare(lower, fmt.Errorf("create containers directory: %w", err))
	}
	if err := os.Mkdir(base, 0700); err != nil {
		return Paths{}, manager.releasePrepare(lower, fmt.Errorf("create container storage %q: %w", base, err))
	}
	for _, directory := range []string{paths.Upper, paths.Work, paths.Merged} {
		if err := os.Mkdir(directory, 0700); err != nil {
			return Paths{}, manager.rollbackPrepare(base, lower, fmt.Errorf("create overlay directory %q: %w", directory, err))
		}
	}
	if err := sameFilesystem(paths.Upper, paths.Work); err != nil {
		return Paths{}, manager.rollbackPrepare(base, lower, fmt.Errorf("validate overlay upper/work filesystem: %w", err))
	}
	manager.mu.Lock()
	manager.owned[base] = true
	manager.mu.Unlock()
	manager.logger.Info("created container overlay layer", "layer", layerName, "lower", lower, "upper", paths.Upper)
	return paths, nil
}

func (manager *Manager) rollbackPrepare(base, lower string, cause error) error {
	if err := os.RemoveAll(base); err != nil {
		cause = errors.Join(cause, fmt.Errorf("remove partial container storage %q: %w", base, err))
	}
	return manager.releasePrepare(lower, cause)
}

func (manager *Manager) releasePrepare(lower string, cause error) error {
	if err := manager.layers.Release(context.Background(), lower); err != nil {
		cause = errors.Join(cause, fmt.Errorf("release failed prepare layer reference: %w", err))
	}
	return cause
}

func (manager *Manager) Mount(ctx context.Context, paths Paths) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}
	if err := validatePaths(paths); err != nil {
		return fmt.Errorf("validate overlay paths: %w", err)
	}
	if err := manager.mounts.MakeMountsPrivate(); err != nil {
		return fmt.Errorf("make child mount tree recursively private before OverlayFS: %w", err)
	}
	manager.logger.Info("made mount tree private before OverlayFS", "recursive", true)
	if err := manager.mounts.MountOverlay(paths.Lower, paths.Upper, paths.Work, paths.Merged); err != nil {
		return fmt.Errorf("mount OverlayFS at %q: %w", paths.Merged, err)
	}
	if err := ctx.Err(); err != nil {
		if unmountErr := manager.mounts.Unmount(paths.Merged); unmountErr != nil {
			return errors.Join(fmt.Errorf("mount canceled: %w", err), fmt.Errorf("rollback OverlayFS mount %q: %w", paths.Merged, unmountErr))
		}
		manager.logger.Info("rolled back OverlayFS mount after cancellation", "merged", paths.Merged)
		return fmt.Errorf("mount OverlayFS canceled: %w", err)
	}
	manager.logger.Info("mounted OverlayFS", "lower", paths.Lower, "upper", paths.Upper, "work", paths.Work, "merged", paths.Merged)
	manager.logger.Debug("OverlayFS will copy up lower files on first write", "upper", paths.Upper)
	return nil
}

func (manager *Manager) Cleanup(ctx context.Context, paths Paths) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("cleanup overlay: %w", err)
	}
	base := filepath.Dir(paths.Upper)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	owned := manager.owned[base]
	if !owned {
		return fmt.Errorf("refusing cleanup of unowned overlay directory %q", base)
	}
	var cleanupErr error
	if err := os.RemoveAll(paths.Merged); err != nil {
		cleanupErr = fmt.Errorf("remove merged mountpoint %q: %w", paths.Merged, err)
	} else if err := manager.layers.Release(ctx, paths.Lower); err != nil {
		cleanupErr = fmt.Errorf("release lower layer %q: %w", paths.Lower, err)
	} else {
		delete(manager.owned, base)
	}
	if cleanupErr == nil {
		manager.logger.Info("cleaned up container overlay mount", "merged", paths.Merged, "upper_preserved", paths.Upper, "mount_namespace_torn_down", true)
	}
	return cleanupErr
}

func (layers *directoryLayers) Resolve(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(layers.root)
	if err != nil {
		return "", fmt.Errorf("resolve layers directory %q: %w", layers.root, err)
	}
	path := filepath.Join(root, name)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("layer resolves outside configured layers directory")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("layer is not a directory")
	}
	return resolved, nil
}

func (layers *directoryLayers) Acquire(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	layers.mu.Lock()
	defer layers.mu.Unlock()
	layers.references[path]++
	return nil
}
func (layers *directoryLayers) Release(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	layers.mu.Lock()
	defer layers.mu.Unlock()
	if layers.references[path] == 0 {
		return fmt.Errorf("layer %q has no active reference", path)
	}
	layers.references[path]--
	return nil
}

func (unixMounts) MountOverlay(lower, upper, work, merged string) error {
	if strings.ContainsAny(lower+upper+work+merged, ",:\\\x00\n") {
		return fmt.Errorf("overlay paths contain unsupported mount-option characters")
	}
	options := "lowerdir=" + lower + ",upperdir=" + upper + ",workdir=" + work
	return unix.Mount("overlay", merged, "overlay", unix.MS_NODEV|unix.MS_NOSUID, options)
}
func (unixMounts) MakeMountsPrivate() error {
	return unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
}
func (unixMounts) Unmount(path string) error { return unix.Unmount(path, unix.MNT_DETACH) }

func sameFilesystem(left, right string) error {
	var leftStat, rightStat unix.Stat_t
	if err := unix.Stat(left, &leftStat); err != nil {
		return err
	}
	if err := unix.Stat(right, &rightStat); err != nil {
		return err
	}
	if leftStat.Dev != rightStat.Dev {
		return fmt.Errorf("upper and work directories are on different filesystems")
	}
	return nil
}

var _ OverlayManager = (*Manager)(nil)
var _ LayerManager = (*directoryLayers)(nil)
var _ MountManager = unixMounts{}
