//go:build linux

package process

import (
	"fmt"
	"os/exec"

	"golang.org/x/sys/unix"
)

func execProgram(args, environment []string) error {
	path, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("resolve executable %q: %w", args[0], err)
	}
	return unix.Exec(path, args, environment)
}
