package ipam

import (
	"context"
	"net/netip"
	"path/filepath"
	"testing"
)

func TestAllocatePersistsSkipsGatewayAndReusesReleasedAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network", "ipam.json")
	subnet := netip.MustParsePrefix("10.88.0.0/29")
	gateway := netip.MustParseAddr("10.88.0.1")
	first, err := New(subnet, gateway, path)
	if err != nil {
		t.Fatal(err)
	}
	addressA, err := first.Allocate(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if addressA.String() != "10.88.0.2/29" {
		t.Fatalf("first allocation = %s", addressA)
	}
	second, err := New(subnet, gateway, path)
	if err != nil {
		t.Fatal(err)
	}
	addressB, err := second.Allocate(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	if addressB.String() != "10.88.0.3/29" {
		t.Fatalf("second allocation = %s", addressB)
	}
	if err := first.Release(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	reused, err := second.Allocate(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if reused != addressA {
		t.Fatalf("reused allocation = %s, want %s", reused, addressA)
	}
}

func TestAllocateIsIdempotentAndExhaustionIsReported(t *testing.T) {
	allocator, err := New(netip.MustParsePrefix("192.0.2.0/30"), netip.MustParseAddr("192.0.2.1"), filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := allocator.Allocate(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	again, err := allocator.Allocate(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("idempotent allocation changed: %s, %s", first, again)
	}
	if _, err := allocator.Allocate(context.Background(), "two"); err == nil {
		t.Fatal("expected subnet exhaustion")
	}
}

func TestRejectsInvalidSubnetsAndGateway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	for _, test := range []struct {
		subnet  string
		gateway string
	}{
		{"2001:db8::/64", "2001:db8::1"},
		{"10.0.0.0/30", "10.0.1.1"},
		{"10.0.0.0/30", "10.0.0.0"},
	} {
		_, err := New(netip.MustParsePrefix(test.subnet), netip.MustParseAddr(test.gateway), path)
		if err == nil {
			t.Fatalf("New(%s, %s) succeeded", test.subnet, test.gateway)
		}
	}
}
