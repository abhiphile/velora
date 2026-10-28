// Package dns handles host resolver discovery and container resolver configuration.
package dns

import (
	"bufio"
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

func ReadHostResolver(path string) (netip.Addr, error) {
	file, err := os.Open(path)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("open host resolver configuration: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		address, err := netip.ParseAddr(fields[1])
		if err == nil && address.Is4() && !address.IsLoopback() {
			return address, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return netip.Addr{}, fmt.Errorf("read host resolver configuration: %w", err)
	}
	return netip.Addr{}, fmt.Errorf("host resolver has no non-loopback IPv4 nameserver")
}

func WriteResolver(ctx context.Context, address netip.Addr) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write container resolver: %w", err)
	}
	if !address.IsValid() || !address.Is4() {
		return fmt.Errorf("container DNS address must be IPv4")
	}
	content := []byte("nameserver " + address.String() + "\noptions timeout:2 attempts:2\n")
	if err := os.WriteFile("/etc/resolv.conf", content, 0644); err != nil {
		return fmt.Errorf("write /etc/resolv.conf: %w", err)
	}
	return nil
}
