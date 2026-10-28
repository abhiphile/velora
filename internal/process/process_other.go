//go:build !linux

package process

import (
	"fmt"
	"os/exec"
)

func configureCommand(*exec.Cmd, uintptr) error {
	return fmt.Errorf("Linux namespaces are only supported on Linux")
}
