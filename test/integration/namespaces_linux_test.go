//go:build linux && integration

package integration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNamespacesAndUTSHostname(t *testing.T) {
	requireRoot(t)
	binary := buildVellora(t)
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		current, err := os.Hostname()
		if err != nil {
			t.Errorf("read host hostname after run: %v", err)
		}
		if current != hostname {
			t.Errorf("host hostname changed from %q to %q", hostname, current)
		}
	}()

	command := exec.Command(binary, "run", "--hostname", "vellora", "--", "/bin/sh", "-c",
		`printf '%s\n' "$$" "$(readlink /proc/self/ns/pid)" "$(readlink /proc/self/ns/uts)" "$(readlink /proc/self/ns/mnt)" "$(readlink /proc/self/ns/ipc)" "$(hostname)"`)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("run namespace probe: %v: %s", err, stderr.String())
	}
	lines := strings.Fields(string(output))
	if len(lines) != 6 {
		t.Fatalf("expected six probe values, got %q", output)
	}
	if lines[0] != "1" {
		t.Errorf("child PID = %s, want 1", lines[0])
	}
	for index, kind := range []string{"pid", "uts", "mnt", "ipc"} {
		hostNamespace, err := os.Readlink(filepath.Join("/proc/self/ns", kind))
		if err != nil {
			t.Fatalf("read host %s namespace: %v", kind, err)
		}
		if lines[index+1] == hostNamespace {
			t.Errorf("%s namespace was not isolated: %s", kind, hostNamespace)
		}
	}
	if lines[5] != "vellora" {
		t.Errorf("container hostname = %q, want vellora", lines[5])
	}
}

func TestMountAndIPCObjectsAreNotVisibleOnHost(t *testing.T) {
	requireRoot(t)
	binary := buildVellora(t)
	t.Run("mount", func(t *testing.T) {
		if _, err := exec.LookPath("mount"); err != nil {
			t.Skip("integration test needs mount")
		}
		directory := t.TempDir()
		mountpoint := filepath.Join(directory, "private-mount")
		ready := filepath.Join(directory, "mount-ready")
		marker := filepath.Join(mountpoint, "inside-only")
		script := fmt.Sprintf("mkdir -p %s && mount -t tmpfs tmpfs %s && touch %s && touch %s && sleep 30", shellQuote(mountpoint), shellQuote(mountpoint), shellQuote(marker), shellQuote(ready))
		done := startRuntime(t, binary, "/bin/sh", "-c", script)
		waitForFile(t, ready, done)
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("mount marker is visible on host: stat err=%v", err)
		}
	})

	t.Run("ipc", func(t *testing.T) {
		for _, executable := range []string{"ipcmk", "ipcs"} {
			if _, err := exec.LookPath(executable); err != nil {
				t.Skipf("integration test needs %s", executable)
			}
		}
		directory := t.TempDir()
		ready := filepath.Join(directory, "ipc-ready")
		result := filepath.Join(directory, "shmid")
		baseline, err := exec.Command("ipcs", "-m").CombinedOutput()
		if err != nil {
			t.Fatalf("list host shared memory before test: %v: %s", err, baseline)
		}
		script := fmt.Sprintf("ipcmk -M 4096 > %s 2>&1 && touch %s && sleep 30", shellQuote(result), shellQuote(ready))
		done := startRuntime(t, binary, "/bin/sh", "-c", script)
		waitForFile(t, ready, done)
		output, err := os.ReadFile(result)
		if err != nil {
			t.Fatalf("read ipcmk output: %v", err)
		}
		match := regexp.MustCompile(`(?m)(?:Shared memory id:|shmid=)\s*([0-9]+)`).FindStringSubmatch(string(output))
		if len(match) != 2 {
			t.Fatalf("could not parse shared memory id from %q", output)
		}
		sharedMemoryID := match[1]
		if sharedMemoryIDs(baseline)[sharedMemoryID] {
			t.Skipf("shared memory ID %s already existed on host; cannot distinguish a namespace collision", sharedMemoryID)
		}
		hostOutput, err := exec.Command("ipcs", "-m").CombinedOutput()
		if err != nil {
			t.Fatalf("list host shared memory after container create: %v: %s", err, hostOutput)
		}
		if sharedMemoryIDs(hostOutput)[sharedMemoryID] {
			t.Fatalf("container shared memory ID %s is visible in host IPC namespace", sharedMemoryID)
		}
	})
}

func buildVellora(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "vellora")
	build := exec.Command("go", "build", "-o", binary, "./cmd/vellora")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build vellora: %v\n%s", err, output)
	}
	return binary
}

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("Linux namespace integration tests require root or equivalent capabilities")
	}
}

func startRuntime(t *testing.T, binary string, args ...string) chan error {
	t.Helper()
	command := exec.Command(binary, append([]string{"run", "--"}, args...)...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start vellora: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("stop runtime process group: %v", err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runtime did not exit after kill")
		}
	})
	return done
}

func waitForFile(t *testing.T, path string, done chan error) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("runtime exited before creating %s: %v", path, err)
		case <-deadline:
			t.Fatalf("timed out waiting for %s", path)
		case <-ticker.C:
		}
	}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func sharedMemoryIDs(output []byte) map[string]bool {
	ids := make(map[string]bool)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[0], "0x") {
			ids[fields[1]] = true
		}
	}
	return ids
}
