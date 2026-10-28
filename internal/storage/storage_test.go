package storage

import "testing"

func TestDefaultConfigHasSeparatedMountTargets(t *testing.T) {
	if err := validateConfig(DefaultConfig()); err != nil {
		t.Fatalf("default storage config: %v", err)
	}
}

func TestConfigRejectsOverlappingOrUnsafeTargets(t *testing.T) {
	config := DefaultConfig()
	config.TmpMount = "/dev/tmp"
	if err := validateConfig(config); err == nil {
		t.Fatal("expected overlapping targets to be rejected")
	}

	config = DefaultConfig()
	config.TmpMount = "/../../outside"
	if err := validateConfig(config); err == nil {
		t.Fatal("expected unclean target to be rejected")
	}

	config = DefaultConfig()
	config.Devices[0].Name = "../host-device"
	if err := validateConfig(config); err == nil {
		t.Fatal("expected device path traversal to be rejected")
	}
}

func TestPathWithinUsesPathBoundaries(t *testing.T) {
	if !isPathWithin("/dev", "/dev/pts") || isPathWithin("/dev", "/device") {
		t.Fatal("path containment did not respect directory boundaries")
	}
}
