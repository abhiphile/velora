package dns

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateResolverFileWritesPrivatePerContainerConfiguration(t *testing.T) {
	root := t.TempDir()
	path, err := CreateResolverFile(root, "container-1", netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "network", "resolv", "container-1.conf")
	if path != want {
		t.Fatalf("resolver path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "nameserver 1.1.1.1") {
		t.Fatalf("resolver content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0400 {
		t.Fatalf("resolver mode = %o, want 400", info.Mode().Perm())
	}
}

func TestCreateResolverFileRejectsUnsafeInputs(t *testing.T) {
	if _, err := CreateResolverFile(t.TempDir(), "../escape", netip.MustParseAddr("1.1.1.1")); err == nil {
		t.Fatal("expected unsafe ID rejection")
	}
	if _, err := CreateResolverFile(t.TempDir(), "valid", netip.MustParseAddr("2001:db8::1")); err == nil {
		t.Fatal("expected IPv6 DNS rejection")
	}
}
