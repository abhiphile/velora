package reference

import (
	"strings"
	"testing"

	image "github.com/vellora/vellora/internal/image"
)

func TestParseTaggedAndDefaultReferences(t *testing.T) {
	parser := NewParser()
	for input, want := range map[string]image.Reference{
		"alpine":                       {Name: "alpine", Tag: "latest"},
		"example.org/team/alpine:3.20": {Name: "example.org/team/alpine", Tag: "3.20"},
	} {
		got, err := parser.Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("Parse(%q) = %+v, want %+v", input, got, want)
		}
	}
}

func TestParseRejectsUnsafeAndMalformedReferences(t *testing.T) {
	parser := NewParser()
	for _, input := range []string{"", "../etc", "a/../b", "/absolute", "a//b", "alpine:", "alpine:bad tag", "alpine@sha512:abcd"} {
		if _, err := parser.Parse(input); err == nil {
			t.Errorf("Parse(%q) succeeded", input)
		}
	}
	if _, err := parser.Parse("alpine@sha256:" + strings.Repeat("a", 64)); err != nil {
		t.Fatalf("valid digest ref: %v", err)
	}
}
