package overlay

import "testing"

func TestLayerNameValidation(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../x", "a/b", "x\\y"} {
		if validName(name) {
			t.Errorf("validName(%q) = true", name)
		}
	}
	if !validName("alpine-3.20") {
		t.Fatal("valid layer name was rejected")
	}
}

func TestPathValidationRequiresCleanAbsolutePaths(t *testing.T) {
	paths := Paths{Lower: "/lower", Upper: "/upper", Work: "/work", Merged: "/merged"}
	if err := validatePaths(paths); err != nil {
		t.Fatalf("valid paths: %v", err)
	}
	paths.Work = "/tmp/../work"
	if err := validatePaths(paths); err == nil {
		t.Fatal("accepted noncanonical path")
	}
}
