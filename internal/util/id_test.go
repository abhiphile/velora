package util

import "testing"

func TestValidID(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{{"web-1", true}, {"", false}, {"../etc", false}, {"has space", false}} {
		if got := ValidID(test.value); got != test.want {
			t.Errorf("ValidID(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestNewIDReturnsValidUniqueIdentifiers(t *testing.T) {
	first, err := NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	second, err := NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if !ValidID(first) || !ValidID(second) {
		t.Fatalf("generated invalid IDs %q and %q", first, second)
	}
	if first == second {
		t.Fatalf("generated duplicate ID %q", first)
	}
}
