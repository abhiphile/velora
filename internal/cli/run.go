package cli

import (
	"fmt"
	"log/slog"
	"net/netip"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/vellora/vellora/internal/cgroup"
	"github.com/vellora/vellora/internal/config"
	"github.com/vellora/vellora/internal/namespace"
	"github.com/vellora/vellora/internal/network"
	"github.com/vellora/vellora/internal/process"
	runtimepkg "github.com/vellora/vellora/internal/runtime"
	"github.com/vellora/vellora/internal/storage"
	"github.com/vellora/vellora/internal/storage/overlay"
)

func newRunCommand(logger *slog.Logger) *cobra.Command {
	var hostname string
	var rootfsPath string
	var configPath string
	var memory string
	var cpu float64
	var pids int64
	command := &cobra.Command{
		Use:   "run [flags] command [args...]",
		Short: "Run a process in isolated Linux namespaces",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runtimeConfig := config.Defaults()
			if configPath != "" {
				loadedConfig, err := config.Load(configPath)
				if err != nil {
					return fmt.Errorf("load runtime configuration: %w", err)
				}
				runtimeConfig = loadedConfig
			}
			if cmd.Flags().Changed("rootfs") {
				runtimeConfig.RootFSPath = rootfsPath
			}
			resources := cgroup.Resources{CPUCores: cpu, PIDsMax: pids}
			if cmd.Flags().Changed("memory") {
				memoryBytes, err := cgroup.ParseMemoryLimit(memory)
				if err != nil {
					return fmt.Errorf("parse --memory: %w", err)
				}
				resources.MemoryBytes = memoryBytes
			}
			if cmd.Flags().Changed("cpu") && cpu <= 0 {
				return fmt.Errorf("--cpu must be greater than zero")
			}
			if cmd.Flags().Changed("pids") && pids <= 0 {
				return fmt.Errorf("--pids must be greater than zero")
			}
			cgroupManager, err := cgroup.NewManager(runtimeConfig.CgroupRoot, logger)
			if err != nil {
				return fmt.Errorf("configure cgroup manager: %w", err)
			}
			rootfsManager, err := storage.NewManager(runtimeConfig.Storage, logger)
			if err != nil {
				return fmt.Errorf("configure rootfs manager: %w", err)
			}
			overlayManager, err := overlay.NewManager(overlay.Config{ImagesRoot: runtimeConfig.ImagePath, RuntimeRoot: runtimeConfig.RuntimeRoot}, logger)
			if err != nil {
				return fmt.Errorf("configure OverlayFS manager: %w", err)
			}
			runner := process.NewRunner(logger)
			networkManager, err := configuredNetworkManager(runtimeConfig, logger)
			if err != nil {
				return fmt.Errorf("configure container network: %w", err)
			}
			manager := runtimepkg.NewManagerWithNetwork(logger, runner, cgroupManager, rootfsManager, overlayManager, networkManager)
			spec := process.Spec{Args: args, Hostname: hostname, RootFS: runtimeConfig.RootFSPath, ConfigPath: configPath}
			set := namespace.Set{PID: true, UTS: true, Mount: true, IPC: true, Network: true}
			return manager.Run(cmd.Context(), spec, set, resources)
		},
	}
	command.Flags().StringVar(&hostname, "hostname", "", "hostname to set inside the UTS namespace")
	command.Flags().StringVar(&rootfsPath, "rootfs", config.Defaults().RootFSPath, "name of an existing base layer under image_path/layers")
	command.Flags().StringVar(&configPath, "config", "", "YAML runtime configuration path")
	command.Flags().StringVar(&memory, "memory", "", "maximum memory usage, for example 512M")
	command.Flags().Float64Var(&cpu, "cpu", 0, "maximum CPU cores, for example 1 or 0.5")
	command.Flags().Int64Var(&pids, "pids", 0, "maximum number of tasks in the cgroup")
	command.Flags().SetInterspersed(false)
	return command
}

func newInitCommand(logger *slog.Logger) *cobra.Command {
	var hostname string
	var rootfsPath string
	var configPath string
	var gateFD int
	var mountNamespace bool
	var utsNamespace bool
	var overlayLower, overlayUpper, overlayWork, overlayMerged string
	var networkID, networkAddress, networkGateway, networkDNS, networkPeer, networkDNSFile, networkSubnet string
	var networkMTU int
	command := &cobra.Command{
		Use:    "__init command [args...]",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := process.WaitForRuntimeSetup(gateFD); err != nil {
				return err
			}
			if !mountNamespace || !utsNamespace {
				return fmt.Errorf("internal initializer requires mount and UTS namespaces")
			}
			runtimeConfig := config.Defaults()
			if configPath != "" {
				loadedConfig, err := config.Load(configPath)
				if err != nil {
					return fmt.Errorf("load initializer configuration: %w", err)
				}
				runtimeConfig = loadedConfig
			}
			rootfsManager, err := storage.NewManager(runtimeConfig.Storage, logger)
			if err != nil {
				return fmt.Errorf("configure initializer rootfs manager: %w", err)
			}
			var networkConfig *network.ContainerConfig
			var networkManager network.NetworkManager
			if networkID != "" {
				address, parseErr := netip.ParsePrefix(networkAddress)
				if parseErr != nil {
					return fmt.Errorf("parse internal network address: %w", parseErr)
				}
				gateway, parseErr := netip.ParseAddr(networkGateway)
				if parseErr != nil {
					return fmt.Errorf("parse internal network gateway: %w", parseErr)
				}
				dnsAddress, parseErr := netip.ParseAddr(networkDNS)
				if parseErr != nil {
					return fmt.Errorf("parse internal network DNS: %w", parseErr)
				}
				subnet, parseErr := netip.ParsePrefix(networkSubnet)
				if parseErr != nil {
					return fmt.Errorf("parse internal network subnet: %w", parseErr)
				}
				networkConfig = &network.ContainerConfig{ID: networkID, IP: address, Gateway: gateway, DNS: dnsAddress, Subnet: subnet, PeerName: networkPeer, MTU: networkMTU, DNSFile: networkDNSFile}
				if networkDNSFile != "" {
					runtimeConfig.RuntimeRoot = filepath.Clean(filepath.Join(filepath.Dir(networkDNSFile), "..", ".."))
				}
				networkManager, err = configuredNetworkManager(runtimeConfig, logger)
				if err != nil {
					return fmt.Errorf("configure initializer network manager: %w", err)
				}
			}
			overlayManager, err := overlay.NewManager(overlay.Config{ImagesRoot: runtimeConfig.ImagePath, RuntimeRoot: runtimeConfig.RuntimeRoot}, logger)
			if err != nil {
				return fmt.Errorf("configure initializer OverlayFS manager: %w", err)
			}
			paths := overlay.Paths{Lower: overlayLower, Upper: overlayUpper, Work: overlayWork, Merged: overlayMerged}
			return runtimepkg.Initialize(cmd.Context(), args, hostname, mountNamespace, utsNamespace, rootfsPath, paths, overlayManager, rootfsManager, networkManager, networkConfig, logger)
		},
	}
	command.Flags().StringVar(&hostname, "hostname", "", "internal namespace hostname")
	command.Flags().StringVar(&rootfsPath, "rootfs", "", "internal merged root filesystem path")
	command.Flags().StringVar(&overlayLower, "overlay-lower", "", "internal OverlayFS lower directory")
	command.Flags().StringVar(&overlayUpper, "overlay-upper", "", "internal OverlayFS upper directory")
	command.Flags().StringVar(&overlayWork, "overlay-work", "", "internal OverlayFS work directory")
	command.Flags().StringVar(&overlayMerged, "overlay-merged", "", "internal OverlayFS merged directory")
	command.Flags().StringVar(&configPath, "config", "", "internal runtime configuration path")
	command.Flags().IntVar(&gateFD, "gate-fd", -1, "internal process start gate descriptor")
	command.Flags().BoolVar(&mountNamespace, "mount-namespace", false, "internal namespace marker")
	command.Flags().BoolVar(&utsNamespace, "uts-namespace", false, "internal namespace marker")
	command.Flags().StringVar(&networkID, "network-id", "", "internal network attachment ID")
	command.Flags().StringVar(&networkAddress, "network-address", "", "internal container IPv4 prefix")
	command.Flags().StringVar(&networkGateway, "network-gateway", "", "internal bridge gateway")
	command.Flags().StringVar(&networkDNS, "network-dns", "", "internal DNS server")
	command.Flags().StringVar(&networkPeer, "network-peer", "", "internal veth peer name")
	command.Flags().IntVar(&networkMTU, "network-mtu", 0, "internal network MTU")
	command.Flags().StringVar(&networkDNSFile, "network-dns-file", "", "internal resolver file")
	command.Flags().StringVar(&networkSubnet, "network-subnet", "", "internal network subnet")
	return command
}
