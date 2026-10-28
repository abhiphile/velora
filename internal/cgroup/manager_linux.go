//go:build linux

package cgroup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type linuxManager struct {
	root   string
	logger *slog.Logger
}

func newManager(root string, logger *slog.Logger) (Manager, error) {
	return &linuxManager{root: filepath.Clean(root), logger: logger}, nil
}

func (manager *linuxManager) Create(ctx context.Context, resources Resources) (*Cgroup, error) {
	if err := resources.Validate(); err != nil {
		return nil, fmt.Errorf("validate cgroup resources: %w", err)
	}
	if resources.Empty() {
		return nil, fmt.Errorf("cannot create a cgroup without resource limits")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("create cgroup: %w", err)
	}
	controllers := requestedControllers(resources)
	if err := manager.prepareRoot(controllers); err != nil {
		return nil, fmt.Errorf("prepare cgroup v2 root %q: %w", manager.root, err)
	}
	path, err := os.MkdirTemp(manager.root, "run-")
	if err != nil {
		return nil, fmt.Errorf("create cgroup below %q: %w", manager.root, err)
	}
	group := &Cgroup{id: filepath.Base(path), path: path}
	if err := manager.applyResources(path, resources); err != nil {
		if cleanupErr := os.Remove(path); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove partial cgroup %q: %w", path, cleanupErr))
		}
		return nil, fmt.Errorf("apply cgroup limits: %w", err)
	}
	manager.logger.Info("created cgroup", "id", group.id, "memory_bytes", resources.MemoryBytes, "cpu_cores", resources.CPUCores, "pids_max", resources.PIDsMax)
	return group, nil
}

func (manager *linuxManager) AddProcess(ctx context.Context, group *Cgroup, pid int) error {
	if err := manager.validateGroup(group); err != nil {
		return err
	}
	if pid < 1 {
		return fmt.Errorf("positive process ID is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("join process %d to cgroup: %w", pid, err)
	}
	if err := os.WriteFile(filepath.Join(group.path, "cgroup.procs"), []byte(fmt.Sprintf("%d", pid)), 0); err != nil {
		return fmt.Errorf("write process %d to %s/cgroup.procs: %w", pid, group.path, err)
	}
	manager.logger.Info("joined process to cgroup", "id", group.id, "pid", pid)
	return nil
}

func (manager *linuxManager) Remove(ctx context.Context, group *Cgroup) error {
	if err := manager.validateGroup(group); err != nil {
		return err
	}
	kernelKill := false
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for cgroup %q to empty: %w", group.id, err)
		}
		populated, err := cgroupPopulated(group.path)
		if err != nil {
			return fmt.Errorf("read cgroup %q events: %w", group.id, err)
		}
		if !populated {
			break
		}
		if !kernelKill {
			var err error
			kernelKill, err = killCgroup(group.path)
			if err != nil {
				return fmt.Errorf("kill remaining tasks in cgroup %q: %w", group.id, err)
			}
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for cgroup %q to empty: %w", group.id, ctx.Err())
		case <-timer.C:
		}
	}
	if err := os.Remove(group.path); err != nil {
		return fmt.Errorf("remove cgroup %q: %w", group.id, err)
	}
	manager.logger.Info("removed cgroup", "id", group.id)
	return nil
}

func (manager *linuxManager) validateGroup(group *Cgroup) error {
	if group == nil || group.path == "" {
		return fmt.Errorf("valid cgroup handle is required")
	}
	if filepath.Dir(filepath.Clean(group.path)) != manager.root {
		return fmt.Errorf("cgroup %q is not a direct child of manager root %q", group.id, manager.root)
	}
	return nil
}

// killCgroup uses the kernel's recursive cgroup.kill interface when present.
// Older cgroup v2 kernels lack that file, so signal each listed process and
// let Remove rescan cgroup.events until all descendants have exited.
func killCgroup(path string) (bool, error) {
	killPath := filepath.Join(path, "cgroup.kill")
	if err := os.WriteFile(killPath, []byte("1"), 0); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	processes, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
	if err != nil {
		return false, fmt.Errorf("read cgroup.procs for cleanup: %w", err)
	}
	for _, value := range strings.Fields(string(processes)) {
		pid, err := strconv.Atoi(value)
		if err != nil || pid < 1 {
			return false, fmt.Errorf("parse process ID %q from cgroup.procs", value)
		}
		if err := unix.Kill(pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
			return false, fmt.Errorf("send SIGKILL to process %d: %w", pid, err)
		}
	}
	return false, nil
}

func (manager *linuxManager) prepareRoot(controllers []string) error {
	parent := filepath.Dir(manager.root)
	var filesystem unix.Statfs_t
	if err := unix.Statfs(parent, &filesystem); err != nil {
		return fmt.Errorf("inspect cgroup mount %q: %w", parent, err)
	}
	if uint64(filesystem.Type) != uint64(unix.CGROUP2_SUPER_MAGIC) {
		return fmt.Errorf("%q is not a cgroup v2 filesystem", parent)
	}
	if err := os.MkdirAll(manager.root, 0755); err != nil {
		return fmt.Errorf("create manager root: %w", err)
	}
	available, err := readWordSet(filepath.Join(manager.root, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("read available controllers: %w", err)
	}
	for _, controller := range controllers {
		if !available[controller] {
			return fmt.Errorf("controller %q is not delegated to %q; enable it in the parent cgroup or configure a delegated cgroup root", controller, manager.root)
		}
	}
	active, err := readWordSet(filepath.Join(manager.root, "cgroup.subtree_control"))
	if err != nil {
		return fmt.Errorf("read enabled controllers: %w", err)
	}
	processes, err := os.ReadFile(filepath.Join(manager.root, "cgroup.procs"))
	if err != nil {
		return fmt.Errorf("read manager cgroup membership: %w", err)
	}
	if strings.TrimSpace(string(processes)) != "" {
		return fmt.Errorf("manager cgroup %q must not contain processes while it distributes controllers to child cgroups", manager.root)
	}
	var enable []string
	for _, controller := range controllers {
		if !active[controller] {
			enable = append(enable, "+"+controller)
		}
	}
	if len(enable) == 0 {
		return nil
	}
	if err := os.WriteFile(filepath.Join(manager.root, "cgroup.subtree_control"), []byte(strings.Join(enable, " ")), 0); err != nil {
		return fmt.Errorf("enable controllers %s: %w", strings.Join(enable, " "), err)
	}
	return nil
}

func (manager *linuxManager) applyResources(path string, resources Resources) error {
	if resources.MemoryBytes > 0 {
		if err := (MemoryController{}).Apply(path, resources.MemoryBytes); err != nil {
			return err
		}
	}
	if resources.CPUCores > 0 {
		if err := (CPUController{}).Apply(path, resources.CPUCores); err != nil {
			return err
		}
	}
	if resources.PIDsMax > 0 {
		if err := (PIDsController{}).Apply(path, resources.PIDsMax); err != nil {
			return err
		}
	}
	return nil
}

func requestedControllers(resources Resources) []string {
	var controllers []string
	if resources.CPUCores > 0 {
		controllers = append(controllers, "cpu")
	}
	if resources.MemoryBytes > 0 {
		controllers = append(controllers, "memory")
	}
	if resources.PIDsMax > 0 {
		controllers = append(controllers, "pids")
	}
	return controllers
}

func readWordSet(path string) (map[string]bool, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	for _, word := range strings.Fields(string(contents)) {
		set[word] = true
	}
	return set, nil
}

func cgroupPopulated(path string) (bool, error) {
	contents, err := os.ReadFile(filepath.Join(path, "cgroup.events"))
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "populated" {
			return fields[1] == "1", nil
		}
	}
	return false, fmt.Errorf("cgroup.events has no populated field")
}
