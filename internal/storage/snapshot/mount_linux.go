//go:build linux

package snapshot

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

type linuxMounts struct{}

func platformMounts() MountManager { return linuxMounts{} }

func (linuxMounts) MountOverlay(lowers []string, upper, work, merged string) error {
	if len(lowers) == 0 {
		return fmt.Errorf("OverlayFS requires at least one lower directory")
	}
	for _, path := range append(append([]string{}, lowers...), upper, work, merged) {
		if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, ",:\x00\n") {
			return fmt.Errorf("invalid OverlayFS path %q", path)
		}
	}
	options := "lowerdir=" + strings.Join(lowers, ":") + ",upperdir=" + upper + ",workdir=" + work
	return unix.Mount("overlay", merged, "overlay", unix.MS_NODEV|unix.MS_NOSUID, options)
}

func (linuxMounts) Unmount(path string) error { return unix.Unmount(path, unix.MNT_DETACH) }
