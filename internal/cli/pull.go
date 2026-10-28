package cli

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vellora/vellora/internal/config"
	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/registry"
	"github.com/vellora/vellora/internal/image/store"
)

func newPullCommand(logger *slog.Logger) *cobra.Command {
	var configPath string
	var maxParallel int
	var platform string
	var insecureRegistries []string
	command := &cobra.Command{
		Use: "pull IMAGE", Short: "Download an OCI or Docker schema 2 image from a registry", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Defaults()
			if configPath != "" {
				loaded, err := config.Load(configPath)
				if err != nil {
					return fmt.Errorf("load runtime configuration: %w", err)
				}
				cfg = loaded
			}
			local, err := store.NewLocal(cfg.ImagePath, logger)
			if err != nil {
				return fmt.Errorf("configure local image store: %w", err)
			}
			selection := image.Platform{}
			if platform != "" {
				var ok bool
				selection, ok = parsePlatform(platform)
				if !ok {
					return fmt.Errorf("platform must use os/architecture[/variant] form")
				}
			}
			puller, err := registry.New(registry.Config{ImageRoot: cfg.ImagePath, Platform: selection, MaxParallel: maxParallel, InsecureRegistries: insecureRegistries}, local, local, nil, logger)
			if err != nil {
				return fmt.Errorf("configure registry client: %w", err)
			}
			item, err := puller.Pull(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("pull image %q: %w", args[0], err)
			}
			return writeImageJSON(cmd.OutOrStdout(), item)
		},
	}
	command.Flags().StringVar(&configPath, "config", "", "YAML runtime configuration path")
	command.Flags().IntVar(&maxParallel, "max-concurrent-downloads", 4, "maximum parallel blob downloads")
	command.Flags().StringVar(&platform, "platform", "", "image platform os/architecture[/variant]")
	command.Flags().StringSliceVar(&insecureRegistries, "insecure-registry", nil, "allow HTTP for this registry host (repeatable)")
	return command
}

func parsePlatform(value string) (image.Platform, bool) {
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return image.Platform{}, false
	}
	platform := image.Platform{OS: parts[0], Architecture: parts[1]}
	if len(parts) == 3 {
		platform.Variant = parts[2]
		if platform.Variant == "" {
			return image.Platform{}, false
		}
	}
	return platform, true
}
