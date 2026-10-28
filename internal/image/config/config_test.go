package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseImageConfig(t *testing.T) {
	value := ImageConfig{Architecture: "amd64", OS: "linux", Config: Runtime{Entrypoint: []string{"/bin/sh"}, WorkingDir: "/app", Env: []string{"PATH=/bin"}}, RootFS: RootFS{Type: "layers", DiffIDs: []string{"sha256:" + strings.Repeat("a", 64)}}}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data, 1)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Architecture != "amd64" || parsed.Config.WorkingDir != "/app" {
		t.Fatalf("unexpected config: %+v", parsed)
	}
}

func TestParseRejectsInvalidRootfsAndEnvironment(t *testing.T) {
	base := `{"architecture":"amd64","os":"linux","config":{"Env":["BROKEN"]},"rootfs":{"type":"layers","diff_ids":[]}}`
	if _, err := Parse([]byte(base), 0); err == nil {
		t.Fatal("accepted invalid environment")
	}
	if _, err := Parse([]byte(strings.Replace(base, `"layers"`, `"unknown"`, 1)), 0); err == nil {
		t.Fatal("accepted unknown rootfs type")
	}
	if _, err := Parse([]byte(base+`{}`), 0); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}
