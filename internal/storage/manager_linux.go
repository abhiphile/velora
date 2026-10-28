//go:build linux

package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type linuxManager struct {
	config Config
	logger *slog.Logger
	files  Filesystem
	mounts MountManager
}

type osFilesystem struct{}

type unixMountManager struct{}

func newManager(config Config, logger *slog.Logger) (RootFSManager, error) {
	return newLinuxManager(config, logger, osFilesystem{}, unixMountManager{}), nil
}

func newLinuxManager(config Config, logger *slog.Logger, files Filesystem, mounts MountManager) *linuxManager {
	return &linuxManager{config: config, logger: logger, files: files, mounts: mounts}
}

func (manager *linuxManager) Prepare(ctx context.Context, rootPath string) (RootFS, error) {
	if err := ctx.Err(); err != nil {
		return RootFS{}, fmt.Errorf("prepare rootfs: %w", err)
	}
	if rootPath == "" {
		return RootFS{}, nil
	}
	absolutePath, err := filepath.Abs(rootPath)
	if err != nil {
		return RootFS{}, fmt.Errorf("resolve rootfs path %q: %w", rootPath, err)
	}
	resolvedPath, err := manager.files.EvalSymlinks(absolutePath)
	if err != nil {
		return RootFS{}, fmt.Errorf("resolve rootfs symlinks %q: %w", absolutePath, err)
	}
	info, err := manager.files.Stat(resolvedPath)
	if err != nil {
		return RootFS{}, fmt.Errorf("inspect rootfs %q: %w", resolvedPath, err)
	}
	if !info.IsDir() {
		return RootFS{}, fmt.Errorf("rootfs %q is not a directory", resolvedPath)
	}
	if resolvedPath == string(filepath.Separator) {
		return RootFS{}, fmt.Errorf("host root cannot be used as a container rootfs")
	}
	for _, target := range []string{manager.config.ProcMount, manager.config.TmpMount, manager.config.DevMount} {
		mountpoint := filepath.Join(resolvedPath, strings.TrimPrefix(target, "/"))
		mountInfo, err := manager.files.Stat(mountpoint)
		if err != nil {
			return RootFS{}, fmt.Errorf("inspect required rootfs mountpoint %q: %w", target, err)
		}
		if !mountInfo.IsDir() {
			return RootFS{}, fmt.Errorf("required rootfs mountpoint %q is not a directory", target)
		}
		resolvedMountpoint, err := manager.files.EvalSymlinks(mountpoint)
		if err != nil {
			return RootFS{}, fmt.Errorf("resolve rootfs mountpoint %q: %w", target, err)
		}
		if filepath.Clean(resolvedMountpoint) != filepath.Clean(mountpoint) {
			return RootFS{}, fmt.Errorf("required rootfs mountpoint %q cannot contain symlinks", target)
		}
	}
	return RootFS{Path: resolvedPath, ReadOnly: true}, nil
}

func (manager *linuxManager) Enter(ctx context.Context, rootfs RootFS) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("enter rootfs: %w", err)
	}
	if err := manager.makeMountsPrivate(); err != nil {
		return err
	}
	if rootfs.Path == "" {
		return manager.mountProc(manager.config.ProcMount)
	}
	if !filepath.IsAbs(rootfs.Path) {
		return fmt.Errorf("rootfs path must be absolute: %q", rootfs.Path)
	}
	return manager.enterRootFS(ctx, rootfs)
}

func (manager *linuxManager) makeMountsPrivate() error {
	if err := manager.mounts.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make inherited mount tree recursively private: %w", err)
	}
	manager.logger.Info("made mount tree private", "recursive", true)
	return nil
}

func (manager *linuxManager) enterRootFS(ctx context.Context, rootfs RootFS) (runErr error) {
	root := filepath.Clean(rootfs.Path)
	rootBound := false
	pivoted := false
	var mounted []string
	defer func() {
		if runErr == nil {
			return
		}
		for index := len(mounted) - 1; index >= 0; index-- {
			if err := manager.mounts.Unmount(mounted[index], unix.MNT_DETACH); err != nil && !errors.Is(err, unix.EINVAL) {
				runErr = errors.Join(runErr, fmt.Errorf("rollback mount %q: %w", mounted[index], err))
			}
		}
		if rootBound && !pivoted {
			if err := manager.mounts.Unmount(root, unix.MNT_DETACH); err != nil && !errors.Is(err, unix.EINVAL) {
				runErr = errors.Join(runErr, fmt.Errorf("rollback rootfs bind mount: %w", err))
			}
		}
		manager.logger.Warn("rootfs setup failed; mount namespace will be discarded", "error", runErr)
	}()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("prepare rootfs mounts: %w", err)
	}
	if err := manager.mounts.Mount(root, root, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind rootfs %q onto itself: %w", root, err)
	}
	rootBound = true
	manager.logger.Info("bind-mounted rootfs", "source", root, "target", root)
	if err := manager.files.Chdir(root); err != nil {
		return fmt.Errorf("change directory to rootfs %q: %w", root, err)
	}
	if err := manager.mounts.PivotRoot(".", "."); err != nil {
		return fmt.Errorf("pivot root to %q: %w", root, err)
	}
	pivoted = true
	manager.logger.Info("pivoted root filesystem", "rootfs", root)
	if err := manager.files.Chdir("/"); err != nil {
		return fmt.Errorf("change directory to new root: %w", err)
	}
	if err := manager.mounts.Unmount(".", unix.MNT_DETACH); err != nil {
		return fmt.Errorf("detach old root mount: %w", err)
	}
	manager.logger.Info("detached old root mount")
	if rootfs.ReadOnly {
		if err := manager.mounts.Mount("", "/", "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
			return fmt.Errorf("remount rootfs read-only: %w", err)
		}
		manager.logger.Info("made rootfs read-only", "rootfs", root)
	}
	if err := manager.mountProc(manager.config.ProcMount); err != nil {
		return err
	}
	mounted = append(mounted, manager.config.ProcMount)
	if err := manager.mounts.Mount("tmpfs", manager.config.TmpMount, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777,size="+manager.config.TmpSize); err != nil {
		return fmt.Errorf("mount private tmpfs at %q: %w", manager.config.TmpMount, err)
	}
	mounted = append(mounted, manager.config.TmpMount)
	manager.logger.Info("mounted private tmpfs", "path", manager.config.TmpMount, "size", manager.config.TmpSize)
	if err := manager.mounts.Mount("tmpfs", manager.config.DevMount, "tmpfs", unix.MS_NOSUID, "mode=0755,size="+manager.config.DevSize); err != nil {
		return fmt.Errorf("mount private dev tmpfs at %q: %w", manager.config.DevMount, err)
	}
	mounted = append(mounted, manager.config.DevMount)
	manager.logger.Info("mounted private dev tmpfs", "path", manager.config.DevMount, "size", manager.config.DevSize)
	if err := manager.mountDev(&mounted); err != nil {
		return err
	}
	return nil
}

func (manager *linuxManager) mountProc(target string) error {
	if err := manager.mounts.Mount("proc", target, "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); err != nil {
		return fmt.Errorf("mount procfs at %q: %w", target, err)
	}
	manager.logger.Info("mounted procfs", "path", target)
	return nil
}

func (manager *linuxManager) mountDev(mounted *[]string) error {
	for _, device := range manager.config.Devices {
		devicePath := filepath.Join(manager.config.DevMount, device.Name)
		if err := manager.files.Mknod(devicePath, unix.S_IFCHR|device.Mode, int(unix.Mkdev(device.Major, device.Minor))); err != nil {
			return fmt.Errorf("create device node %q: %w", devicePath, err)
		}
	}
	ptsPath := manager.config.PtsMount
	if err := manager.files.MkdirAll(ptsPath, 0755); err != nil {
		return fmt.Errorf("create devpts mountpoint: %w", err)
	}
	if err := manager.mounts.Mount("devpts", ptsPath, "devpts", unix.MS_NOSUID|unix.MS_NOEXEC, "newinstance,ptmxmode=0666,mode=0620"); err != nil {
		return fmt.Errorf("mount devpts at %q: %w", ptsPath, err)
	}
	*mounted = append(*mounted, ptsPath)
	ptsRelative, err := filepath.Rel(manager.config.DevMount, manager.config.PtsMount)
	if err != nil {
		return fmt.Errorf("resolve devpts path relative to dev mount: %w", err)
	}
	if err := manager.files.Symlink(filepath.Join(ptsRelative, "ptmx"), filepath.Join(manager.config.DevMount, "ptmx")); err != nil {
		return fmt.Errorf("create /dev/ptmx link: %w", err)
	}
	shmPath := manager.config.ShmMount
	if err := manager.files.MkdirAll(shmPath, 01777); err != nil {
		return fmt.Errorf("create /dev/shm directory: %w", err)
	}
	if err := manager.mounts.Mount("tmpfs", shmPath, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777,size="+manager.config.ShmSize); err != nil {
		return fmt.Errorf("mount private tmpfs at %q: %w", shmPath, err)
	}
	*mounted = append(*mounted, shmPath)
	manager.logger.Info("mounted private shared-memory tmpfs", "path", shmPath, "size", manager.config.ShmSize)
	return nil
}

func (osFilesystem) EvalSymlinks(value string) (string, error) { return filepath.EvalSymlinks(value) }
func (osFilesystem) Stat(value string) (fs.FileInfo, error)    { return os.Stat(value) }
func (osFilesystem) MkdirAll(value string, mode fs.FileMode) error {
	return os.MkdirAll(value, mode)
}
func (osFilesystem) Chdir(value string) error              { return os.Chdir(value) }
func (osFilesystem) Symlink(oldPath, newPath string) error { return os.Symlink(oldPath, newPath) }
func (osFilesystem) Mknod(value string, mode uint32, device int) error {
	return unix.Mknod(value, mode, device)
}

func (unixMountManager) Mount(source, target, filesystem string, flags uintptr, data string) error {
	return unix.Mount(source, target, filesystem, flags, data)
}
func (unixMountManager) PivotRoot(newRoot, putOld string) error {
	return unix.PivotRoot(newRoot, putOld)
}
func (unixMountManager) Unmount(target string, flags int) error { return unix.Unmount(target, flags) }

var _ MountManager = unixMountManager{}

var _ Filesystem = osFilesystem{}
