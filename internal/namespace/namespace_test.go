package namespace

import "testing"

func TestSetDefaultsToNoIsolation(t *testing.T) {
	if (Set{}) != (Set{PID: false, UTS: false, Mount: false, Network: false, IPC: false, User: false}) {
		t.Fatal("zero-value namespace set should request no isolation")
	}
}
