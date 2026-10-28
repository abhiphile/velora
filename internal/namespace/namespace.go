// Package namespace defines the namespace set requested for a runtime process.
package namespace

// Set describes which Linux namespaces a process should enter.
type Set struct{ PID, UTS, Mount, Network, IPC, User bool }

// Flags returns clone(2) flags for the requested namespaces on Linux.
func (set Set) Flags() (uintptr, error) { return namespaceFlags(set) }

// ConfigureChild applies namespace-local settings before the requested program starts.
func ConfigureChild(hostname string) error {
	return configureChild(hostname)
}
