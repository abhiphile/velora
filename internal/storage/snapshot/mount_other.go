//go:build !linux

package snapshot

import "fmt"

type unsupportedMounts struct{}

func platformMounts() MountManager { return unsupportedMounts{} }
func (unsupportedMounts) MountOverlay([]string, string, string, string) error {
	return fmt.Errorf("snapshot OverlayFS is only supported on Linux")
}
func (unsupportedMounts) Unmount(string) error {
	return fmt.Errorf("snapshot OverlayFS is only supported on Linux")
}
