package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOverlaysDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("log_level: debug\nnetwork_bridge: test0\nstorage:\n  tmp_size: 32m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.LogLevel != "debug" || got.NetworkBridge != "test0" || got.RuntimeRoot == "" || got.Storage.TmpSize != "32m" || got.Storage.DevMount != "/dev" {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestLoadShippedRuntimeConfiguration(t *testing.T) {
	configPath := filepath.Join("..", "..", "configs", "vellora.yaml")
	got, err := Load(configPath)
	if err != nil {
		t.Fatalf("load shipped configuration: %v", err)
	}
	if got.Storage.ProcMount != "/proc" || got.Storage.TmpMount != "/tmp" || len(got.Storage.Devices) == 0 {
		t.Fatalf("unexpected storage configuration: %+v", got.Storage)
	}
}
