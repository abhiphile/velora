//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/vellora/vellora/internal/network/bridge"
	"github.com/vellora/vellora/internal/network/command"
	"github.com/vellora/vellora/internal/network/dns"
	"github.com/vellora/vellora/internal/network/ipam"
	containerNamespace "github.com/vellora/vellora/internal/network/namespace"
	"github.com/vellora/vellora/internal/network/nat"
	"github.com/vellora/vellora/internal/network/route"
	"github.com/vellora/vellora/internal/network/veth"
	"github.com/vellora/vellora/internal/util"
)

type linuxManager struct {
	config    Config
	logger    *slog.Logger
	ipam      IPAM
	bridge    BridgeManager
	veth      *veth.Manager
	nat       NATManager
	routes    RouteManager
	mu        sync.Mutex
	connected map[string]bool
}

var validBridgeName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

func NewManager(config Config, logger *slog.Logger) (NetworkManager, error) {
	if logger == nil {
		return nil, fmt.Errorf("network manager requires a logger")
	}
	if config.RuntimeRoot == "" {
		return nil, fmt.Errorf("network runtime root is required")
	}
	absoluteRuntimeRoot, err := filepath.Abs(config.RuntimeRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve network runtime root: %w", err)
	}
	config.RuntimeRoot = absoluteRuntimeRoot
	if config.Bridge == "" {
		config.Bridge = "vellora0"
	}
	if !validBridgeName.MatchString(config.Bridge) || config.Bridge == "." || config.Bridge == ".." {
		return nil, fmt.Errorf("invalid bridge name %q", config.Bridge)
	}
	if !config.Subnet.IsValid() {
		config.Subnet = netip.MustParsePrefix("10.88.0.0/16")
	}
	config.Subnet = config.Subnet.Masked()
	if !config.Subnet.Addr().Is4() {
		return nil, fmt.Errorf("network subnet must be IPv4")
	}
	if !config.Gateway.IsValid() {
		config.Gateway = firstHost(config.Subnet)
	}
	if !config.DNS.IsValid() {
		if address, err := dns.ReadHostResolver("/etc/resolv.conf"); err == nil {
			config.DNS = address
		} else {
			config.DNS = netip.MustParseAddr("8.8.8.8")
			logger.Warn("using fallback DNS resolver", "dns", config.DNS.String(), "reason", err)
		}
	}
	if !config.DNS.Is4() {
		return nil, fmt.Errorf("network DNS address must be IPv4")
	}
	if config.MTU == 0 {
		config.MTU = 1500
	}
	if config.MTU < 576 || config.MTU > 65535 {
		return nil, fmt.Errorf("network MTU must be between 576 and 65535 bytes")
	}
	if config.IPBinary == "" {
		config.IPBinary = "ip"
	}
	if config.IPTablesBinary == "" {
		config.IPTablesBinary = "iptables"
	}
	executor := command.OS{}
	allocator, err := ipam.New(config.Subnet, config.Gateway, filepath.Join(config.RuntimeRoot, "network", "ipam.json"))
	if err != nil {
		return nil, err
	}
	bridges, err := bridge.New(executor, config.IPBinary, "sysctl", logger)
	if err != nil {
		return nil, err
	}
	links, err := veth.New(executor, config.IPBinary, logger)
	if err != nil {
		return nil, err
	}
	firewall, err := nat.New(executor, config.IPTablesBinary, logger)
	if err != nil {
		return nil, err
	}
	namespaceConfigurator, err := containerNamespace.New(executor, config.IPBinary, logger)
	if err != nil {
		return nil, err
	}
	routes, err := route.New(namespaceConfigurator, logger)
	if err != nil {
		return nil, err
	}
	return &linuxManager{config: config, logger: logger, ipam: allocator, bridge: bridges, veth: links, nat: firewall, routes: linuxRouteManager{manager: routes}, connected: make(map[string]bool)}, nil
}

func (manager *linuxManager) Prepare(ctx context.Context, id string) (Attachment, error) {
	if !util.ValidID(id) {
		return Attachment{}, fmt.Errorf("invalid container network ID %q", id)
	}
	address, err := manager.ipam.Allocate(ctx, id)
	if err != nil {
		return Attachment{}, err
	}
	hostName, peerName := linkNames(id)
	resolverFile, err := dns.CreateResolverFile(manager.config.RuntimeRoot, id, manager.config.DNS)
	if err != nil {
		_ = manager.ipam.Release(context.Background(), id)
		return Attachment{}, err
	}
	attachment := Attachment{Config: ContainerConfig{ID: id, IP: address, Gateway: manager.config.Gateway, DNS: manager.config.DNS, Subnet: manager.config.Subnet, PeerName: peerName, MTU: manager.config.MTU, DNSFile: resolverFile}, HostVeth: hostName}
	manager.logger.Info("allocated container IP address", "container", id, "address", address.String(), "subnet", manager.config.Subnet.String())
	return attachment, nil
}

func (manager *linuxManager) Connect(ctx context.Context, attachment Attachment, pid int) error {
	if attachment.Config.ID == "" || pid <= 0 {
		return fmt.Errorf("container attachment and PID are required")
	}
	if err := manager.bridge.Ensure(ctx, manager.config.Bridge, manager.config.Subnet, manager.config.Gateway, manager.config.MTU); err != nil {
		return err
	}
	if err := manager.nat.Ensure(ctx, manager.config.Bridge, manager.config.Subnet.String(), ""); err != nil {
		return fmt.Errorf("configure NAT: %w", err)
	}
	if err := manager.veth.CreateAndMove(ctx, attachment.HostVeth, attachment.Config.PeerName, manager.config.Bridge, pid, manager.config.MTU); err != nil {
		return err
	}
	manager.mu.Lock()
	manager.connected[attachment.Config.ID] = true
	manager.mu.Unlock()
	manager.logger.Info("connected container to bridge", "container", attachment.Config.ID, "pid", pid, "address", attachment.Config.IP.String())
	return nil
}

func (manager *linuxManager) ConfigureContainer(ctx context.Context, config ContainerConfig) error {
	return manager.routes.Configure(ctx, config)
}

func (manager *linuxManager) ConfigureDNS(ctx context.Context, config ContainerConfig, rootFS string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("configure container DNS: %w", err)
	}
	if err := dns.BindResolverFile(config.DNSFile, rootFS); err != nil {
		return err
	}
	manager.logger.Info("configured container resolver", "container", config.ID, "dns", config.DNS.String())
	return nil
}

func (manager *linuxManager) Disconnect(ctx context.Context, attachment Attachment) error {
	expected := filepath.Join(manager.config.RuntimeRoot, "network", "resolv", attachment.Config.ID+".conf")
	if attachment.Config.DNSFile != expected {
		return fmt.Errorf("refusing to remove unexpected resolver path %q", attachment.Config.DNSFile)
	}
	manager.mu.Lock()
	connected := manager.connected[attachment.Config.ID]
	manager.mu.Unlock()
	if connected {
		if err := manager.veth.Delete(ctx, attachment.HostVeth); err != nil {
			return fmt.Errorf("delete container veth: %w", err)
		}
	}
	if err := manager.ipam.Release(ctx, attachment.Config.ID); err != nil {
		return fmt.Errorf("release container IP: %w", err)
	}
	if err := os.Remove(expected); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove container resolver file: %w", err)
	}
	manager.mu.Lock()
	delete(manager.connected, attachment.Config.ID)
	manager.mu.Unlock()
	manager.logger.Info("cleaned up container network attachment", "container", attachment.Config.ID, "veth", attachment.HostVeth)
	return nil
}

func (manager *linuxManager) List(ctx context.Context) ([]NetworkInfo, error) {
	allocations, err := manager.ipam.List(ctx)
	if err != nil {
		return nil, err
	}
	return []NetworkInfo{manager.info(len(allocations))}, nil
}
func (manager *linuxManager) Inspect(ctx context.Context, name string) (NetworkInfo, error) {
	if name != manager.config.Bridge {
		return NetworkInfo{}, fmt.Errorf("unknown network %q", name)
	}
	allocations, err := manager.ipam.List(ctx)
	if err != nil {
		return NetworkInfo{}, err
	}
	return manager.info(len(allocations)), nil
}
func (manager *linuxManager) Create(ctx context.Context, name string) error {
	if name != manager.config.Bridge {
		return fmt.Errorf("only configured bridge %q can be created", manager.config.Bridge)
	}
	if err := manager.bridge.Ensure(ctx, name, manager.config.Subnet, manager.config.Gateway, manager.config.MTU); err != nil {
		return err
	}
	return manager.nat.Ensure(ctx, name, manager.config.Subnet.String(), "")
}
func (manager *linuxManager) Remove(ctx context.Context, name string) error {
	if name != manager.config.Bridge {
		return fmt.Errorf("unknown network %q", name)
	}
	allocations, err := manager.ipam.List(ctx)
	if err != nil {
		return err
	}
	if len(allocations) > 0 {
		return fmt.Errorf("cannot remove network %q while %d container(s) are allocated", name, len(allocations))
	}
	if err := manager.nat.Remove(ctx, name); err != nil {
		return err
	}
	if manager.bridge.Exists(ctx, name) {
		return manager.bridge.Delete(ctx, name)
	}
	return nil
}
func (manager *linuxManager) info(count int) NetworkInfo {
	return NetworkInfo{Name: manager.config.Bridge, Bridge: manager.config.Bridge, Subnet: manager.config.Subnet.String(), Gateway: manager.config.Gateway.String(), DNS: manager.config.DNS.String(), MTU: manager.config.MTU, Allocations: count}
}

func firstHost(subnet netip.Prefix) netip.Addr {
	bytes := subnet.Masked().Addr().As4()
	raw := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	raw++
	return netip.AddrFrom4([4]byte{byte(raw >> 24), byte(raw >> 16), byte(raw >> 8), byte(raw)})
}
func linkNames(id string) (string, string) {
	suffix := id
	if len(suffix) > 11 {
		suffix = suffix[:11]
	}
	return "vh" + suffix, "vc" + suffix
}

type linuxRouteManager struct{ manager *route.Manager }

func (adapter linuxRouteManager) Configure(ctx context.Context, config ContainerConfig) error {
	return adapter.manager.Configure(ctx, containerNamespace.Config{PeerName: config.PeerName, Address: config.IP.String(), Gateway: config.Gateway.String(), MTU: config.MTU})
}

var _ NetworkManager = (*linuxManager)(nil)
var _ RouteManager = linuxRouteManager{}
