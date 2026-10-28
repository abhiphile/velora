//go:build !linux

package process

import "fmt"

func execProgram([]string, []string) error {
	return fmt.Errorf("Linux namespaces are only supported on Linux")
}
