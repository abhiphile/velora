//go:build !linux

package dns

import "fmt"

func BindResolverFile(string, string) error {
	return fmt.Errorf("resolver bind mounts are only supported on Linux")
}
