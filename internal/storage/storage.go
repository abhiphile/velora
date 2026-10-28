// Package storage prepares private root filesystems for container processes.
package storage

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// RootFS is a canonicalized root tree supplied by the caller.
type RootFS struct {
	Path     string
	ReadOnly bool
}

// Device describes a character device to create inside the private /dev tmpfs.
type Device struct {
	Name  string `yaml:"name"`
	Mode  uint32 `yaml:"mode"`
	Major uint32 `yaml:"major"`
	Minor uint32 `yaml:"minor"`
}

// Config controls container mount targets and temporary filesystem sizing.
type Config struct {
	ProcMount string   `yaml:"proc_mount"`
	TmpMount  string   `yaml:"tmp_mount"`
	DevMount  string   `yaml:"dev_mount"`
	PtsMount  string   `yaml:"pts_mount"`
	ShmMount  string   `yaml:"shm_mount"`
	TmpSize   string   `yaml:"tmp_size"`
	DevSize   string   `yaml:"dev_size"`
	ShmSize   string   `yaml:"shm_size"`
	Devices   []Device `yaml:"devices"`
}

// DefaultConfig returns conventional in-container mount paths and a minimal device set.
func DefaultConfig() Config {
	return Config{
		ProcMount: "/proc",
		TmpMount:  "/tmp",
		DevMount:  "/dev",
		PtsMount:  "/dev/pts",
		ShmMount:  "/dev/shm",
		TmpSize:   "64m",
		DevSize:   "16m",
		ShmSize:   "64m",
		Devices: []Device{
			{Name: "null", Mode: 0666, Major: 1, Minor: 3},
			{Name: "zero", Mode: 0666, Major: 1, Minor: 5},
			{Name: "full", Mode: 0666, Major: 1, Minor: 7},
			{Name: "random", Mode: 0666, Major: 1, Minor: 8},
			{Name: "urandom", Mode: 0666, Major: 1, Minor: 9},
			{Name: "tty", Mode: 0666, Major: 5, Minor: 0},
		},
	}
}

// RootFSManager validates supplied trees and installs them in a process mount namespace.
type RootFSManager interface {
	Prepare(context.Context, string) (RootFS, error)
	Enter(context.Context, RootFS) error
}

// Filesystem abstracts path operations needed for rootfs setup and rollback.
type Filesystem interface {
	EvalSymlinks(string) (string, error)
	Stat(string) (fs.FileInfo, error)
	MkdirAll(string, fs.FileMode) error
	Chdir(string) error
	Symlink(string, string) error
	Mknod(string, uint32, int) error
}

// MountManager abstracts Linux mount operations from rootfs orchestration.
type MountManager interface {
	Mount(string, string, string, uintptr, string) error
	PivotRoot(string, string) error
	Unmount(string, int) error
}

// NewManager creates a platform-specific root filesystem manager.
func NewManager(config Config, logger *slog.Logger) (RootFSManager, error) {
	if logger == nil {
		return nil, fmt.Errorf("rootfs manager requires a logger")
	}
	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("validate rootfs mount configuration: %w", err)
	}
	return newManager(config, logger)
}

func validateConfig(config Config) error {
	for name, path := range map[string]string{
		"proc mount": config.ProcMount,
		"tmp mount":  config.TmpMount,
		"dev mount":  config.DevMount,
		"pts mount":  config.PtsMount,
		"shm mount":  config.ShmMount,
	} {
		if path == "" || path[0] != '/' || path != cleanAbsolute(path) {
			return fmt.Errorf("%s must be a clean absolute path: %q", name, path)
		}
		if path == "/" {
			return fmt.Errorf("%s cannot be the root directory", name)
		}
	}
	if pathsOverlap(config.ProcMount, config.TmpMount) || pathsOverlap(config.ProcMount, config.DevMount) || pathsOverlap(config.TmpMount, config.DevMount) {
		return fmt.Errorf("proc, tmp, and dev mount targets must not overlap")
	}
	if !isPathWithin(config.DevMount, config.PtsMount) || !isPathWithin(config.DevMount, config.ShmMount) || pathsOverlap(config.PtsMount, config.ShmMount) {
		return fmt.Errorf("pts and shm mount targets must be separate descendants of dev")
	}
	if config.TmpSize == "" || config.DevSize == "" || config.ShmSize == "" {
		return fmt.Errorf("tmpfs sizes must be configured")
	}
	if !validateSize(config.TmpSize) || !validateSize(config.DevSize) || !validateSize(config.ShmSize) {
		return fmt.Errorf("tmpfs sizes must be a number optionally followed by k, m, or g")
	}
	seenDevices := make(map[string]bool, len(config.Devices))
	for _, device := range config.Devices {
		if device.Name == "" || device.Name == "." || device.Name == ".." || strings.ContainsAny(device.Name, "/\x00") {
			return fmt.Errorf("device name must be a simple path component: %q", device.Name)
		}
		if device.Name == "pts" || device.Name == "shm" || device.Name == "ptmx" || device.Mode&^0777 != 0 {
			return fmt.Errorf("device %q conflicts with a managed path or has invalid permission bits", device.Name)
		}
		if seenDevices[device.Name] {
			return fmt.Errorf("device name %q is configured more than once", device.Name)
		}
		seenDevices[device.Name] = true
	}
	return nil
}

func cleanAbsolute(value string) string {
	return path.Clean(value)
}

func pathsOverlap(left, right string) bool {
	return left == right || isPathWithin(left, right) || isPathWithin(right, left)
}

func isPathWithin(parent, child string) bool {
	return strings.HasPrefix(child, strings.TrimSuffix(parent, "/")+"/")
}

var sizePattern = regexp.MustCompile(`^[0-9]+[kKmMgG]?$`)

func validateSize(value string) bool {
	if !sizePattern.MatchString(value) {
		return false
	}
	number := strings.TrimRight(value, "kKmMgG")
	parsed, err := strconv.ParseUint(number, 10, 64)
	return err == nil && parsed > 0
}
