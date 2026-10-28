//go:build linux

package process

import (
	"os/exec"
	"syscall"
)

func configureCommand(command *exec.Cmd, flags uintptr) error {
	command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: flags}
	return nil
}
