package dns

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
)

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// CreateResolverFile writes a per-container resolver configuration under runtime state.
func CreateResolverFile(runtimeRoot, id string, address netip.Addr) (string, error) {
	if runtimeRoot == "" || !safeID.MatchString(id) || !address.IsValid() || !address.Is4() {
		return "", fmt.Errorf("invalid resolver file configuration")
	}
	directory := filepath.Join(runtimeRoot, "network", "resolv")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", fmt.Errorf("create resolver directory: %w", err)
	}
	path := filepath.Join(directory, id+".conf")
	content := []byte("nameserver " + address.String() + "\noptions timeout:2 attempts:2\n")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return "", fmt.Errorf("write resolver file: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write resolver file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("sync resolver file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close resolver file: %w", err)
	}
	return path, nil
}
