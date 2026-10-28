//go:build linux

package cgroup

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

func TestValidateGroupRequiresDirectChild(t *testing.T) {
	root := t.TempDir()
	manager := &linuxManager{root: root, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := manager.validateGroup(&Cgroup{id: "run-test", path: filepath.Join(root, "run-test")}); err != nil {
		t.Fatalf("validate direct child: %v", err)
	}
	for _, group := range []*Cgroup{nil, {}, {id: "nested", path: filepath.Join(root, "nested", "group")}} {
		if err := manager.validateGroup(group); err == nil {
			t.Errorf("validateGroup(%+v) unexpectedly succeeded", group)
		}
	}
}
