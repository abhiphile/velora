// Package runtime coordinates process and subsystem operations.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/vellora/vellora/internal/cgroup"
	"github.com/vellora/vellora/internal/namespace"
	"github.com/vellora/vellora/internal/network"
	"github.com/vellora/vellora/internal/process"
	"github.com/vellora/vellora/internal/storage"
	"github.com/vellora/vellora/internal/storage/overlay"
	"github.com/vellora/vellora/internal/util"
)

// Runtime defines the first runnable lifecycle operation.
type Runtime interface {
	Run(context.Context, process.Spec, namespace.Set, cgroup.Resources) error
}

// Manager sequences runtime operations while keeping process creation injectable.
type Manager struct {
	logger  *slog.Logger
	runner  process.Runner
	cgroups cgroup.Manager
	rootfs  storage.RootFSManager
	overlay overlay.OverlayManager
	network network.NetworkManager
}

// NewManagerWithNetwork adds an optional network coordinator while preserving
// NewManager for callers that do not configure container networking.
func NewManagerWithNetwork(logger *slog.Logger, runner process.Runner, cgroups cgroup.Manager, rootfs storage.RootFSManager, overlays overlay.OverlayManager, networks network.NetworkManager) *Manager {
	manager := NewManager(logger, runner, cgroups, rootfs, overlays)
	manager.network = networks
	return manager
}

// NewManager creates a runtime manager with its process backend and logger.
func NewManager(logger *slog.Logger, runner process.Runner, cgroups cgroup.Manager, rootfs storage.RootFSManager, overlays ...overlay.OverlayManager) *Manager {
	manager := &Manager{logger: logger, runner: runner, cgroups: cgroups, rootfs: rootfs}
	if len(overlays) > 0 {
		manager.overlay = overlays[0]
	}
	return manager
}

// Run starts a process with the requested namespace set.
func (manager *Manager) Run(ctx context.Context, spec process.Spec, namespaces namespace.Set, resources cgroup.Resources) (runErr error) {
	if manager == nil {
		return fmt.Errorf("runtime manager is not configured")
	}
	if manager.logger == nil {
		return fmt.Errorf("runtime logger is not configured")
	}
	if manager.runner == nil {
		return fmt.Errorf("runtime process runner is not configured")
	}
	if len(spec.Args) == 0 {
		return fmt.Errorf("runtime process arguments must include an executable")
	}
	if !namespaces.Mount {
		return fmt.Errorf("a private mount namespace is required for filesystem setup")
	}
	if manager.rootfs == nil {
		return fmt.Errorf("runtime rootfs manager is not configured")
	}
	var overlayPaths overlay.Paths
	var containerID string
	var err error
	var attachment network.Attachment
	if manager.network != nil {
		var err error
		containerID, err = util.NewID()
		if err != nil {
			return fmt.Errorf("generate container ID: %w", err)
		}
		attachment, err = manager.network.Prepare(ctx, containerID)
		if err != nil {
			return fmt.Errorf("prepare container networking: %w", err)
		}
		spec.NetworkConfig = &attachment.Config
		namespaces.Network = true
		defer func() {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := manager.network.Disconnect(cleanupContext, attachment); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("cleanup container networking: %w", err))
			}
		}()
	}
	if spec.RootFS != "" {
		if manager.overlay == nil {
			return fmt.Errorf("overlay manager is required when --rootfs is specified")
		}
		if containerID == "" {
			containerID, err = util.NewID()
			if err != nil {
				return fmt.Errorf("generate container ID: %w", err)
			}
		}
		overlayPaths, err = manager.overlay.Prepare(ctx, containerID, spec.RootFS)
		if err != nil {
			return fmt.Errorf("prepare container overlay: %w", err)
		}
		spec.RootFS = overlayPaths.Merged
		spec.OverlayLower, spec.OverlayUpper, spec.OverlayWork, spec.OverlayMerged = overlayPaths.Lower, overlayPaths.Upper, overlayPaths.Work, overlayPaths.Merged
		defer func() {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := manager.overlay.Cleanup(cleanupContext, overlayPaths); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("cleanup container overlay: %w", err))
			}
		}()
	}
	manager.logger.Debug("runtime run requested", "executable", spec.Args[0])
	if err := resources.Validate(); err != nil {
		return fmt.Errorf("runtime resource limits: %w", err)
	}
	if resources.Empty() {
		var beforeExec func(int) error
		if manager.network != nil {
			beforeExec = func(pid int) error { return manager.network.Connect(ctx, attachment, pid) }
		}
		err := manager.runner.Run(ctx, spec, namespaces, beforeExec)
		if spec.RootFS != "" {
			manager.logger.Info("container filesystem namespace exited", "rootfs", spec.RootFS)
		}
		if err != nil {
			return fmt.Errorf("runtime run: %w", err)
		}
		return nil
	}
	if manager.cgroups == nil {
		return fmt.Errorf("runtime cgroup manager is not configured")
	}
	group, err := manager.cgroups.Create(ctx, resources)
	if err != nil {
		return fmt.Errorf("create runtime cgroup: %w", err)
	}
	if group == nil {
		return fmt.Errorf("create runtime cgroup: manager returned an empty handle")
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := manager.cgroups.Remove(cleanupContext, group); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("cleanup runtime cgroup %q: %w", group.ID(), err))
		}
	}()
	manager.logger.Info("applying resource limits", "cgroup", group.ID(), "memory_bytes", resources.MemoryBytes, "cpu_cores", resources.CPUCores, "pids_max", resources.PIDsMax)
	joinProcess := func(pid int) error {
		if manager.network != nil {
			if err := manager.network.Connect(ctx, attachment, pid); err != nil {
				return err
			}
		}
		return manager.cgroups.AddProcess(ctx, group, pid)
	}
	err = manager.runner.Run(ctx, spec, namespaces, joinProcess)
	if spec.RootFS != "" {
		manager.logger.Info("container filesystem namespace exited", "rootfs", spec.RootFS)
	}
	if err != nil {
		return fmt.Errorf("runtime run: %w", err)
	}
	return nil
}

// Initialize prepares namespaces and mounts inside the re-executed helper before exec.
func Initialize(ctx context.Context, args []string, hostname string, mountNamespace, utsNamespace bool, rootFSPath string, overlayPaths overlay.Paths, overlays overlay.OverlayManager, rootfs storage.RootFSManager, networks network.NetworkManager, networkConfig *network.ContainerConfig, logger *slog.Logger) error {
	if len(args) == 0 {
		return fmt.Errorf("runtime initializer requires a command")
	}
	if rootfs == nil {
		return fmt.Errorf("runtime rootfs manager is not configured")
	}
	if !mountNamespace || !utsNamespace {
		return fmt.Errorf("runtime initializer requires mount and UTS namespaces")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("initialize runtime process: %w", err)
	}
	if err := namespace.ConfigureChild(hostname); err != nil {
		return fmt.Errorf("configure child namespace: %w", err)
	}
	if networkConfig != nil {
		if networks == nil {
			return fmt.Errorf("network manager is not configured in initializer")
		}
		if err := networks.ConfigureContainer(ctx, *networkConfig); err != nil {
			return fmt.Errorf("configure container network namespace: %w", err)
		}
	}
	if overlayPaths.Merged != "" {
		if overlays == nil {
			return fmt.Errorf("overlay manager is not configured in initializer")
		}
		if err := overlays.Mount(ctx, overlayPaths); err != nil {
			return fmt.Errorf("mount container overlay: %w", err)
		}
	}
	preparedRootFS, err := rootfs.Prepare(ctx, rootFSPath)
	if err != nil {
		return fmt.Errorf("prepare child rootfs: %w", err)
	}
	if overlayPaths.Merged != "" {
		preparedRootFS.ReadOnly = false
	}
	if networkConfig != nil {
		if err := networks.ConfigureDNS(ctx, *networkConfig, preparedRootFS.Path); err != nil {
			return fmt.Errorf("configure container DNS resolver: %w", err)
		}
	}
	if err := rootfs.Enter(ctx, preparedRootFS); err != nil {
		return fmt.Errorf("enter child rootfs: %w", err)
	}
	environment := os.Environ()
	if preparedRootFS.Path != "" {
		environment = setEnvironment(environment, "PWD", "/")
	}
	if err := process.Execute(args, environment); err != nil {
		return fmt.Errorf("execute workload: %w", err)
	}
	return nil
}

func setEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	replaced := false
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				result = append(result, prefix+value)
				replaced = true
			}
			continue
		}
		result = append(result, entry)
	}
	if !replaced {
		result = append(result, prefix+value)
	}
	return result
}
