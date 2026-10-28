// Package network coordinates host bridge, veth, IPAM, and firewall operations.
package network

import (
	"context"
	"net/netip"
)

type Config struct {
	RuntimeRoot    string
	Bridge         string
	Subnet         netip.Prefix
	Gateway        netip.Addr
	DNS            netip.Addr
	MTU            int
	IPBinary       string
	IPTablesBinary string
}

type ContainerConfig struct {
	ID       string
	IP       netip.Prefix
	Gateway  netip.Addr
	DNS      netip.Addr
	Subnet   netip.Prefix
	PeerName string
	MTU      int
	DNSFile  string
}

type NetworkInfo struct {
	Name        string `json:"name"`
	Bridge      string `json:"bridge"`
	Subnet      string `json:"subnet"`
	Gateway     string `json:"gateway"`
	DNS         string `json:"dns"`
	MTU         int    `json:"mtu"`
	Allocations int    `json:"allocations"`
}

type Attachment struct {
	Config   ContainerConfig
	HostVeth string
}

type NetworkManager interface {
	Prepare(context.Context, string) (Attachment, error)
	Connect(context.Context, Attachment, int) error
	ConfigureContainer(context.Context, ContainerConfig) error
	ConfigureDNS(context.Context, ContainerConfig, string) error
	Disconnect(context.Context, Attachment) error
	List(context.Context) ([]NetworkInfo, error)
	Inspect(context.Context, string) (NetworkInfo, error)
	Create(context.Context, string) error
	Remove(context.Context, string) error
}

type BridgeManager interface {
	Ensure(context.Context, string, netip.Prefix, netip.Addr, int) error
	Delete(context.Context, string) error
	Exists(context.Context, string) bool
}
type IPAM interface {
	Allocate(context.Context, string) (netip.Prefix, error)
	Release(context.Context, string) error
	List(context.Context) (map[string]netip.Prefix, error)
}
type NATManager interface {
	Ensure(context.Context, string, string, string) error
	Remove(context.Context, string) error
}
type RouteManager interface {
	Configure(context.Context, ContainerConfig) error
}
