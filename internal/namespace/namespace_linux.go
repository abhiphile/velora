//go:build linux

package namespace

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func namespaceFlags(set Set) (uintptr, error) {
	var flags uintptr
	if set.PID {
		flags |= unix.CLONE_NEWPID
	}
	if set.UTS {
		flags |= unix.CLONE_NEWUTS
	}
	if set.Mount {
		flags |= unix.CLONE_NEWNS
	}
	if set.IPC {
		flags |= unix.CLONE_NEWIPC
	}
	if set.Network {
		flags |= unix.CLONE_NEWNET
	}
	if set.User {
		return 0, fmt.Errorf("user namespaces are outside this milestone")
	}
	return flags, nil
}

func configureChild(hostname string) error {
	if hostname != "" {
		if err := unix.Sethostname([]byte(hostname)); err != nil {
			return fmt.Errorf("set namespace hostname: %w", err)
		}
	}
	return nil
}
