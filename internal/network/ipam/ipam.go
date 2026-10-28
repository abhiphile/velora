// Package ipam provides persistent IPv4 allocation for container endpoints.
package ipam

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Allocator struct {
	subnet  netip.Prefix
	gateway netip.Addr
	path    string
	mu      sync.Mutex
}
type state struct {
	Allocations map[string]string `json:"allocations"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func New(subnet netip.Prefix, gateway netip.Addr, path string) (*Allocator, error) {
	if !subnet.IsValid() || !subnet.Addr().Is4() || subnet.Bits() < 8 || subnet.Bits() > 30 {
		return nil, fmt.Errorf("IPAM requires an IPv4 subnet with prefix length between /8 and /30")
	}
	subnet = subnet.Masked()
	if !gateway.Is4() || !subnet.Contains(gateway) {
		return nil, fmt.Errorf("gateway must be inside the IPv4 subnet")
	}
	first := binary.BigEndian.Uint32(subnet.Addr().AsSlice())
	last := first | (uint32(1) << (32 - subnet.Bits())) - 1
	address := binary.BigEndian.Uint32(gateway.AsSlice())
	if address == first || address == last {
		return nil, fmt.Errorf("gateway cannot be the network or broadcast address")
	}
	if path == "" {
		return nil, fmt.Errorf("IPAM state path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve IPAM state path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, fmt.Errorf("create IPAM state directory: %w", err)
	}
	return &Allocator{subnet: subnet, gateway: gateway, path: absolute}, nil
}

func (allocator *Allocator) Allocate(ctx context.Context, id string) (netip.Prefix, error) {
	if id == "" {
		return netip.Prefix{}, fmt.Errorf("IPAM allocation ID is required")
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	var result netip.Prefix
	err := allocator.withState(ctx, func(value *state) error {
		if existing, ok := value.Allocations[id]; ok {
			address, err := netip.ParseAddr(existing)
			if err != nil {
				return fmt.Errorf("invalid persisted allocation for %q: %w", id, err)
			}
			result = netip.PrefixFrom(address, allocator.subnet.Bits())
			return nil
		}
		used := make(map[string]bool, len(value.Allocations)+2)
		used[allocator.subnet.Addr().String()] = true
		used[allocator.gateway.String()] = true
		for _, address := range value.Allocations {
			used[address] = true
		}
		address := allocator.subnet.Addr().As4()
		base := binary.BigEndian.Uint32(address[:])
		hostBits := 32 - allocator.subnet.Bits()
		limit := uint32(1) << hostBits
		for offset := uint32(1); offset < limit-1; offset++ {
			raw := base + offset
			candidate := netip.AddrFrom4([4]byte{byte(raw >> 24), byte(raw >> 16), byte(raw >> 8), byte(raw)})
			if !used[candidate.String()] {
				value.Allocations[id] = candidate.String()
				result = netip.PrefixFrom(candidate, allocator.subnet.Bits())
				return nil
			}
		}
		return fmt.Errorf("no free addresses in %s", allocator.subnet)
	})
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("allocate container address: %w", err)
	}
	return result, nil
}

func (allocator *Allocator) Release(ctx context.Context, id string) error {
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	err := allocator.withState(ctx, func(value *state) error {
		if _, ok := value.Allocations[id]; !ok {
			return fmt.Errorf("no IPAM allocation for %q", id)
		}
		delete(value.Allocations, id)
		return nil
	})
	if err != nil {
		return fmt.Errorf("release container address: %w", err)
	}
	return nil
}
func (allocator *Allocator) List(ctx context.Context) (map[string]netip.Prefix, error) {
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	result := make(map[string]netip.Prefix)
	err := allocator.withState(ctx, func(value *state) error {
		for id, value := range value.Allocations {
			address, err := netip.ParseAddr(value)
			if err != nil {
				return err
			}
			result[id] = netip.PrefixFrom(address, allocator.subnet.Bits())
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list IPAM allocations: %w", err)
	}
	return result, nil
}

func (allocator *Allocator) withState(ctx context.Context, update func(*state) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := os.OpenFile(allocator.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	value := state{Allocations: make(map[string]string)}
	data, err := os.ReadFile(allocator.path)
	if err == nil {
		if err := json.Unmarshal(data, &value); err != nil {
			return fmt.Errorf("decode IPAM state: %w", err)
		}
		if value.Allocations == nil {
			value.Allocations = make(map[string]string)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := update(&value); err != nil {
		return err
	}
	value.UpdatedAt = time.Now().UTC()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(allocator.path), ".ipam-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, allocator.path); err != nil {
		return err
	}
	return nil
}

var _ interface {
	Allocate(context.Context, string) (netip.Prefix, error)
	Release(context.Context, string) error
	List(context.Context) (map[string]netip.Prefix, error)
} = (*Allocator)(nil)
