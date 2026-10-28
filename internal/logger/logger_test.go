package logger

import (
	"bytes"
	"testing"
)

func TestNewWritesStructuredLog(t *testing.T) {
	var output bytes.Buffer
	New(&output).Info("ready", "component", "test")
	if output.Len() == 0 {
		t.Fatal("expected log output")
	}
}
