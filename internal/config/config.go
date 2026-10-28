package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vellora/vellora/internal/storage"
	"gopkg.in/yaml.v3"
)

// Config contains host-level defaults used by the runtime and its subsystems.
type Config struct {
	RuntimeRoot     string            `yaml:"runtime_root"`
	CgroupRoot      string            `yaml:"cgroup_root"`
	RootFSPath      string            `yaml:"rootfs_path"`
	Storage         storage.Config    `yaml:"storage"`
	ImagePath       string            `yaml:"image_path"`
	NetworkBridge   string            `yaml:"network_bridge"`
	NetworkSubnet   string            `yaml:"network_subnet"`
	NetworkGateway  string            `yaml:"network_gateway"`
	NetworkDNS      string            `yaml:"network_dns"`
	NetworkMTU      int               `yaml:"network_mtu"`
	LogLevel        string            `yaml:"log_level"`
	RuntimeSettings map[string]string `yaml:"runtime_settings,omitempty"`
}

// Defaults returns platform-appropriate paths without creating them.
func Defaults() Config {
	return Config{
		RuntimeRoot:    filepath.Join(os.TempDir(), "vellora"),
		CgroupRoot:     "/sys/fs/cgroup/vellora",
		RootFSPath:     "",
		Storage:        storage.DefaultConfig(),
		ImagePath:      filepath.Join(os.TempDir(), "vellora", "images"),
		NetworkBridge:  "vellora0",
		NetworkSubnet:  "10.88.0.0/16",
		NetworkGateway: "10.88.0.1",
		NetworkDNS:     "",
		NetworkMTU:     1500,
		LogLevel:       "info",
	}
}

// Load reads YAML configuration and overlays it on built-in defaults.
func Load(path string) (Config, error) {
	config := Defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}
	return config, nil
}
