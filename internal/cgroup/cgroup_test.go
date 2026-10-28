package cgroup

import "testing"

func TestResourcesZeroValueHasNoLimits(t *testing.T) {
	if resources := (Resources{}); !resources.Empty() {
		t.Fatalf("unexpected zero-value resources: %+v", resources)
	}
}

func TestParseMemoryLimit(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int64
	}{
		{"512M", 512 << 20}, {"1GiB", 1 << 30}, {"4096", 4096},
	} {
		got, err := ParseMemoryLimit(test.value)
		if err != nil {
			t.Fatalf("ParseMemoryLimit(%q): %v", test.value, err)
		}
		if got != test.want {
			t.Errorf("ParseMemoryLimit(%q) = %d, want %d", test.value, got, test.want)
		}
	}
}

func TestParseMemoryRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "0", "-1M", "512Z", "999999999999999999999T"} {
		if _, err := ParseMemoryLimit(value); err == nil {
			t.Errorf("ParseMemoryLimit(%q) unexpectedly succeeded", value)
		}
	}
}
