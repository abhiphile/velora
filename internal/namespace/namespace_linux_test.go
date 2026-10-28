//go:build linux

package namespace

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestFlagsSelectRequestedNamespaces(t *testing.T) {
	flags, err := (Set{PID: true, UTS: true, Mount: true, IPC: true}).Flags()
	if err != nil {
		t.Fatal(err)
	}
	want := uintptr(unix.CLONE_NEWPID | unix.CLONE_NEWUTS | unix.CLONE_NEWNS | unix.CLONE_NEWIPC)
	if flags != want {
		t.Fatalf("Flags() = %#x, want %#x", flags, want)
	}
}

func TestFlagsRejectOutOfScopeNamespaces(t *testing.T) {
	if _, err := (Set{User: true}).Flags(); err == nil {
		t.Fatal("expected user namespace rejection")
	}
}

func TestFlagsSelectNetworkNamespace(t *testing.T) {
	flags, err := (Set{Network: true}).Flags()
	if err != nil {
		t.Fatal(err)
	}
	if flags != uintptr(unix.CLONE_NEWNET) {
		t.Fatalf("Flags() = %#x, want CLONE_NEWNET", flags)
	}
}
