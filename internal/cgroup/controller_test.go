package cgroup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryControllerWritesBytes(t *testing.T) {
	path := cgroupFixture(t, "memory.max")
	if err := (MemoryController{}).Apply(path, 512<<20); err != nil {
		t.Fatal(err)
	}
	assertControllerValue(t, path, "memory.max", "536870912")
}

func TestCPUControllerWritesQuotaAndPeriod(t *testing.T) {
	path := cgroupFixture(t, "cpu.max")
	if err := (CPUController{}).Apply(path, 0.5); err != nil {
		t.Fatal(err)
	}
	assertControllerValue(t, path, "cpu.max", "50000 100000")
}

func TestPIDsControllerWritesMaximum(t *testing.T) {
	path := cgroupFixture(t, "pids.max")
	if err := (PIDsController{}).Apply(path, 64); err != nil {
		t.Fatal(err)
	}
	assertControllerValue(t, path, "pids.max", "64")
}

func TestCPUControllerRejectsTooSmallQuota(t *testing.T) {
	if err := (CPUController{}).Apply(t.TempDir(), 0.001); err == nil {
		t.Fatal("expected sub-millisecond CPU quota rejection")
	}
}

func cgroupFixture(t *testing.T, name string) string {
	t.Helper()
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, name), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertControllerValue(t *testing.T, path, name, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(path, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}
