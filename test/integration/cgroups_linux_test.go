//go:build linux && integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRunAppliesAndRemovesCgroupLimits(t *testing.T) {
	requireRoot(t)
	var filesystem unix.Statfs_t
	if err := unix.Statfs("/sys/fs/cgroup", &filesystem); err != nil {
		t.Fatal(err)
	}
	if uint64(filesystem.Type) != uint64(unix.CGROUP2_SUPER_MAGIC) {
		t.Skip("host does not use the cgroups v2 unified hierarchy")
	}
	controllers, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := os.ReadFile("/sys/fs/cgroup/cgroup.subtree_control")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cpu", "memory", "pids"} {
		if !strings.Contains(" "+string(controllers)+" ", " "+name+" ") {
			t.Skipf("host does not expose the %s controller", name)
		}
		if !strings.Contains(" "+string(enabled)+" ", " "+name+" ") {
			t.Skipf("host has not enabled the %s controller for child cgroups", name)
		}
	}

	binary := buildVellora(t)
	script := `cgpath=""; while IFS=: read -r hierarchy controllers path; do if [ "$hierarchy" = 0 ]; then cgpath=$path; fi; done < /proc/self/cgroup; printf '%s\n' "$$" "$cgpath"; cat "/sys/fs/cgroup${cgpath}/memory.max"; cat "/sys/fs/cgroup${cgpath}/cpu.max"; cat "/sys/fs/cgroup${cgpath}/pids.max"`
	command := exec.Command(binary, "run", "--memory=64M", "--cpu=0.5", "--pids=16", "--", "/bin/sh", "-c", script)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("run with cgroup limits: %v", err)
	}
	values := strings.Fields(string(output))
	if len(values) != 5 {
		t.Fatalf("expected PID, cgroup path, and three limits; got %q", output)
	}
	if values[0] != "1" {
		t.Errorf("container PID = %q, want 1", values[0])
	}
	if values[2] != "67108864" {
		t.Errorf("memory.max = %q, want 67108864", values[2])
	}
	if values[3] != "50000 100000" {
		t.Errorf("cpu.max = %q, want 50000 100000", values[3])
	}
	if values[4] != "16" {
		t.Errorf("pids.max = %q, want 16", values[4])
	}
	path := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(values[1], "/"))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cgroup directory remains after exit: %s (stat err %v)", path, err)
	}
}
