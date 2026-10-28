package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/spf13/cobra"
	"github.com/vellora/vellora/internal/config"
	"github.com/vellora/vellora/internal/network"
)

func configuredNetworkManager(runtimeConfig config.Config, logger *slog.Logger) (network.NetworkManager, error) {
	subnet, err := netip.ParsePrefix(runtimeConfig.NetworkSubnet)
	if err != nil {
		return nil, fmt.Errorf("parse network_subnet: %w", err)
	}
	var gateway netip.Addr
	if runtimeConfig.NetworkGateway != "" {
		gateway, err = netip.ParseAddr(runtimeConfig.NetworkGateway)
		if err != nil {
			return nil, fmt.Errorf("parse network_gateway: %w", err)
		}
	}
	var dnsAddress netip.Addr
	if runtimeConfig.NetworkDNS != "" {
		dnsAddress, err = netip.ParseAddr(runtimeConfig.NetworkDNS)
		if err != nil {
			return nil, fmt.Errorf("parse network_dns: %w", err)
		}
	}
	return network.NewManager(network.Config{
		RuntimeRoot: runtimeConfig.RuntimeRoot,
		Bridge:      runtimeConfig.NetworkBridge,
		Subnet:      subnet,
		Gateway:     gateway,
		DNS:         dnsAddress,
		MTU:         runtimeConfig.NetworkMTU,
	}, logger)
}

func newNetworkCommand(logger *slog.Logger) *cobra.Command {
	var configPath string
	group := &cobra.Command{Use: "network", Short: "Manage Vellora host networking"}
	group.PersistentFlags().StringVar(&configPath, "config", "", "YAML runtime configuration path")
	manager := func() (network.NetworkManager, string, error) {
		runtimeConfig := config.Defaults()
		if configPath != "" {
			loaded, err := config.Load(configPath)
			if err != nil {
				return nil, "", err
			}
			runtimeConfig = loaded
		}
		configured, err := configuredNetworkManager(runtimeConfig, logger)
		return configured, runtimeConfig.NetworkBridge, err
	}
	group.AddCommand(&cobra.Command{Use: "ls", Short: "List configured networks", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		configured, _, err := manager()
		if err != nil {
			return err
		}
		items, err := configured.List(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(items)
	}})
	group.AddCommand(&cobra.Command{Use: "inspect <name>", Short: "Show network configuration", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		configured, _, err := manager()
		if err != nil {
			return err
		}
		item, err := configured.Inspect(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(item)
	}})
	group.AddCommand(&cobra.Command{Use: "create", Short: "Create the configured bridge network", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		configured, bridgeName, err := manager()
		if err != nil {
			return err
		}
		return configured.Create(cmd.Context(), bridgeName)
	}})
	group.AddCommand(&cobra.Command{Use: "rm <name>", Short: "Remove the configured bridge network", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		configured, _, err := manager()
		if err != nil {
			return err
		}
		return configured.Remove(cmd.Context(), args[0])
	}})
	return group
}
