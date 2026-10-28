// Package process owns process creation and replacement under runtime control.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"

	"github.com/vellora/vellora/internal/namespace"
	"github.com/vellora/vellora/internal/network"
)

// Spec describes a process independently of the CLI representation.
type Spec struct {
	Args             []string
	Environment      []string
	WorkingDirectory string
	Hostname         string
	RootFS           string
	ConfigPath       string
	OverlayLower     string
	OverlayUpper     string
	OverlayWork      string
	OverlayMerged    string
	NetworkConfig    *network.ContainerConfig
}

// Runner starts a helper and invokes beforeExec while it is gated, before workload code can run.
type Runner interface {
	Run(context.Context, Spec, namespace.Set, func(int) error) error
}

// ExecRunner starts the current executable as a namespace initializer.
type ExecRunner struct{ logger *slog.Logger }

// NewRunner creates a process runner with an injected structured logger.
func NewRunner(logger *slog.Logger) *ExecRunner { return &ExecRunner{logger: logger} }

// Run creates the helper process with Linux clone flags and waits for it to exit.
func (runner *ExecRunner) Run(ctx context.Context, spec Spec, namespaces namespace.Set, beforeExec func(int) error) error {
	if runner == nil || runner.logger == nil {
		return fmt.Errorf("process runner requires a logger")
	}
	if len(spec.Args) == 0 || spec.Args[0] == "" {
		return fmt.Errorf("process arguments must include an executable")
	}
	flags, err := namespaces.Flags()
	if err != nil {
		return fmt.Errorf("prepare namespaces: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate vellora executable: %w", err)
	}
	childGate, parentGate, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create process start gate: %w", err)
	}
	defer childGate.Close()
	defer parentGate.Close()
	args := []string{"__init", "--hostname", spec.Hostname, "--rootfs", spec.RootFS, "--gate-fd", "3"}
	if spec.OverlayMerged != "" {
		args = append(args, "--overlay-lower", spec.OverlayLower, "--overlay-upper", spec.OverlayUpper, "--overlay-work", spec.OverlayWork, "--overlay-merged", spec.OverlayMerged)
	}
	if spec.NetworkConfig != nil {
		netConfig := spec.NetworkConfig
		args = append(args, "--network-id", netConfig.ID, "--network-address", netConfig.IP.String(), "--network-gateway", netConfig.Gateway.String(), "--network-dns", netConfig.DNS.String(), "--network-subnet", netConfig.Subnet.String(), "--network-peer", netConfig.PeerName, "--network-mtu", fmt.Sprint(netConfig.MTU), "--network-dns-file", netConfig.DNSFile)
	}
	if namespaces.Mount {
		args = append(args, "--mount-namespace")
	}
	if namespaces.UTS {
		args = append(args, "--uts-namespace")
	}
	if spec.ConfigPath != "" {
		args = append(args, "--config", spec.ConfigPath)
	}
	args = append(args, "--")
	args = append(args, spec.Args...)
	command := exec.CommandContext(ctx, self, args...)
	command.ExtraFiles = []*os.File{childGate}
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.Dir = spec.WorkingDirectory
	command.Env = spec.Environment
	if command.Env == nil {
		command.Env = os.Environ()
	}
	if err := configureCommand(command, flags); err != nil {
		return fmt.Errorf("configure isolated process: %w", err)
	}
	runner.logger.Info("starting process with namespaces", "command", spec.Args[0], "pid_namespace", namespaces.PID, "uts_namespace", namespaces.UTS, "mount_namespace", namespaces.Mount, "ipc_namespace", namespaces.IPC, "network_namespace", namespaces.Network)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start isolated process %q: %w", spec.Args[0], err)
	}
	if err := childGate.Close(); err != nil {
		return errors.Join(fmt.Errorf("close child start gate: %w", err), abortChild(command))
	}
	if beforeExec != nil {
		if err := beforeExec(command.Process.Pid); err != nil {
			return errors.Join(fmt.Errorf("prepare process %d before exec: %w", command.Process.Pid, err), abortChild(command))
		}
	}
	if _, err := parentGate.Write([]byte{1}); err != nil {
		return errors.Join(fmt.Errorf("release process start gate: %w", err), abortChild(command))
	}
	if err := parentGate.Close(); err != nil {
		return errors.Join(fmt.Errorf("close parent start gate: %w", err), abortChild(command))
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("run isolated process %q: %w", spec.Args[0], err)
	}
	return nil
}

// WaitForRuntimeSetup blocks the namespace helper until its parent has configured host-side controls.
func WaitForRuntimeSetup(gateFD int) error {
	if err := waitForGate(gateFD); err != nil {
		return fmt.Errorf("wait for runtime setup: %w", err)
	}
	return nil
}

// Execute replaces the initializer with the requested workload process.
func Execute(args, environment []string) error {
	if len(args) == 0 {
		return fmt.Errorf("workload requires a command")
	}
	return execProgram(args, environment)
}

func abortChild(command *exec.Cmd) error {
	if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill blocked helper: %w", err)
	}
	if err := command.Wait(); err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			return fmt.Errorf("wait for blocked helper: %w", err)
		}
	}
	return nil
}

func waitForGate(fd int) error {
	if fd < 3 {
		return fmt.Errorf("invalid start gate file descriptor %d", fd)
	}
	gate := os.NewFile(uintptr(fd), "vellora-start-gate")
	if gate == nil {
		return fmt.Errorf("open start gate file descriptor %d", fd)
	}
	defer gate.Close()
	var signal [1]byte
	if _, err := io.ReadFull(gate, signal[:]); err != nil {
		return fmt.Errorf("read start gate: %w", err)
	}
	if signal[0] != 1 {
		return fmt.Errorf("invalid start gate signal %d", signal[0])
	}
	return nil
}
