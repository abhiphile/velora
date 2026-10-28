//go:build !linux

package namespace

import "fmt"

func namespaceFlags(Set) (uintptr, error) {
	return 0, fmt.Errorf("Linux namespaces are only supported on Linux")
}
func configureChild(string) error {
	return fmt.Errorf("Linux namespaces are only supported on Linux")
}
