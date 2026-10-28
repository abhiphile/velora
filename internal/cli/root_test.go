package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/vellora/vellora/internal/image/testutil"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootCommandIncludesPlannedCommands(t *testing.T) {
	command := NewRootCommand(slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, name := range []string{"run", "exec", "ps", "stop", "kill", "rm", "images", "pull", "logs", "stats", "inspect", "snapshot", "network"} {
		if command.Commands() == nil {
			t.Fatal("no commands registered")
		}
		if _, _, err := command.Find([]string{name}); err != nil {
			t.Errorf("command %q missing: %v", name, err)
		}
	}
}

func TestNetworkCommandsAreRegistered(t *testing.T) {
	root := NewRootCommand(slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, name := range [][]string{{"network", "ls"}, {"network", "inspect"}, {"network", "create"}, {"network", "rm"}} {
		if _, _, err := root.Find(name); err != nil {
			t.Errorf("command %v missing: %v", name, err)
		}
	}
}

func TestImageCommandsImportListAndInspectLocalLayout(t *testing.T) {
	imageRoot := filepath.Join(t.TempDir(), "image-store")
	layoutRoot := filepath.Join(t.TempDir(), "oci-layout")
	testutil.Create(t, layoutRoot)
	configPath := filepath.Join(t.TempDir(), "vellora.yaml")
	if err := os.WriteFile(configPath, []byte("image_path: "+imageRoot+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, args := range [][]string{
		{"image", "import", "alpine", layoutRoot, "--config", configPath},
		{"images", "--config", configPath},
		{"image", "ls", "--config", configPath},
		{"inspect", "alpine", "--config", configPath},
		{"image", "inspect", "alpine", "--config", configPath},
	} {
		command := NewRootCommand(logger)
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(io.Discard)
		command.SetArgs(args)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("execute %v: %v", args, err)
		}
		if len(args) == 2 && args[0] == "images" {
			continue
		}
		if strings.Contains(strings.Join(args, " "), "inspect") {
			var value map[string]any
			if err := json.Unmarshal(output.Bytes(), &value); err != nil {
				t.Fatalf("inspect output for %v is not JSON: %v\n%s", args, err, output.String())
			}
			if value["architecture"] != "amd64" || value["os"] != "linux" {
				t.Fatalf("inspect omitted platform: %s", output.String())
			}
		} else if !strings.Contains(output.String(), "alpine:latest") {
			t.Fatalf("list output for %v omitted image reference: %s", args, output.String())
		}
	}
}
