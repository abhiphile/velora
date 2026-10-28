//go:build linux

package dns

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// BindResolverFile places an isolated, read-only resolver file in this mount namespace.
func BindResolverFile(source, rootFS string) error {
	if source == "" {
		return fmt.Errorf("resolver source path is required")
	}
	target := "/etc/resolv.conf"
	var root string
	if rootFS != "" {
		resolvedRoot, err := filepath.Abs(rootFS)
		if err != nil {
			return fmt.Errorf("resolve container rootfs: %w", err)
		}
		root = resolvedRoot
		etc := filepath.Join(root, "etc")
		if err := os.MkdirAll(etc, 0755); err != nil {
			return fmt.Errorf("create container /etc: %w", err)
		}
		target = filepath.Join(etc, "resolv.conf")
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, resolveErr := filepath.EvalSymlinks(target)
			if resolveErr != nil {
				return fmt.Errorf("resolve resolver target symlink: %w", resolveErr)
			}
			if root != "" {
				relative, relErr := filepath.Rel(root, resolved)
				if relErr != nil || relative == ".." || filepath.IsAbs(relative) || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
					return fmt.Errorf("resolver target symlink escapes container rootfs")
				}
			}
			target = resolved
			info, err = os.Lstat(target)
			if err != nil {
				return fmt.Errorf("inspect resolved resolver target: %w", err)
			}
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("resolver target %q must be a regular file", target)
		}
	} else if os.IsNotExist(err) {
		file, createErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
		if createErr != nil {
			return fmt.Errorf("create resolver target: %w", createErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close resolver target: %w", closeErr)
		}
	} else {
		return fmt.Errorf("inspect resolver target: %w", err)
	}
	if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind resolver file: %w", err)
	}
	if err := unix.Mount("", target, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		_ = unix.Unmount(target, unix.MNT_DETACH)
		return fmt.Errorf("make resolver mount read-only: %w", err)
	}
	return nil
}
