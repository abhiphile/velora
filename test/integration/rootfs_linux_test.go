//go:build linux && integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUsesPrivateOverlayRootFS(t *testing.T) {
	requireRoot(t)
	binary := buildVellora(t)
	imageRoot := t.TempDir()
	runtimeRoot := t.TempDir()
	rootfs := filepath.Join(imageRoot, "layers", "test-base")
	for _, name := range []string{"bin", "proc", "tmp", "dev"} {
		if err := os.MkdirAll(filepath.Join(rootfs, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	immutablePath := filepath.Join(rootfs, "host-source-file")
	if err := os.WriteFile(immutablePath, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	hostMarker := filepath.Join(t.TempDir(), "host-only-marker")
	if err := os.WriteFile(hostMarker, []byte("host"), 0644); err != nil {
		t.Fatal(err)
	}
	probeSource := filepath.Join(t.TempDir(), "probe.go")
	probe := `package main
import (
  "fmt"
  "os"
)
func main() {
  cwd, err := os.Getwd(); if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
  _, hostErr := os.Stat(os.Args[1])
  writeErr := os.WriteFile("/host-source-file", []byte("changed"), 0644)
  tmpErr := os.WriteFile("/tmp/vellora-probe", []byte("private"), 0600)
  fmt.Printf("cwd=%s\nhost_visible=%t\nroot_write_succeeded=%t\ntmp_write=%t\n", cwd, hostErr == nil, writeErr == nil, tmpErr == nil)
}`
	if err := os.WriteFile(probeSource, []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	buildProbe := exec.Command("go", "build", "-o", filepath.Join(rootfs, "probe"), probeSource)
	buildProbe.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := buildProbe.CombinedOutput(); err != nil {
		t.Fatalf("build static rootfs probe: %v\n%s", err, output)
	}

	configPath := filepath.Join(t.TempDir(), "vellora.yaml")
	configText := "runtime_root: " + runtimeRoot + "\nimage_path: " + imageRoot + "\n"
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "run", "--config", configPath, "--rootfs", "test-base", "--", "/probe", hostMarker)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run with private rootfs: %v\n%s", err, output)
	}
	for _, expected := range []string{"cwd=/", "host_visible=false", "root_write_succeeded=true", "tmp_write=true"} {
		if !strings.Contains(string(output), expected) {
			t.Errorf("rootfs probe output lacks %q:\n%s", expected, output)
		}
	}
	contents, err := os.ReadFile(immutablePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "original" {
		t.Errorf("lower layer file changed: %q", contents)
	}
	upperFiles, err := filepath.Glob(filepath.Join(runtimeRoot, "containers", "*", "upper", "host-source-file"))
	if err != nil {
		t.Fatal(err)
	}
	if len(upperFiles) != 1 {
		t.Fatalf("expected copy-up in one upper layer, found %v", upperFiles)
	}
	upperContents, err := os.ReadFile(upperFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(upperContents) != "changed" {
		t.Errorf("upper file contents = %q, want changed", upperContents)
	}
	if _, err := os.Stat(filepath.Join(rootfs, "tmp", "vellora-probe")); !os.IsNotExist(err) {
		t.Errorf("container tmpfs content persisted in rootfs: stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(rootfs, ".vellora-oldroot")); !os.IsNotExist(err) {
		t.Errorf("pivot_root created an unexpected staging path in source rootfs: stat err=%v", err)
	}
}
