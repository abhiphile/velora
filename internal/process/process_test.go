package process

import "testing"

func TestSpecPreservesProcessInputs(t *testing.T) {
	spec := Spec{Args: []string{"echo", "hello"}, Environment: []string{"MODE=test"}, WorkingDirectory: "/tmp"}
	if spec.Args[0] != "echo" || spec.Environment[0] != "MODE=test" || spec.WorkingDirectory != "/tmp" {
		t.Fatalf("unexpected process spec: %+v", spec)
	}
}
