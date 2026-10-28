package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vellora/vellora/internal/config"
	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/reference"
	"github.com/vellora/vellora/internal/image/store"
)

func newImageCommand(logger *slog.Logger) *cobra.Command {
	command := &cobra.Command{Use: "image", Short: "Manage local OCI images"}
	command.AddCommand(newImageListCommand(logger, "ls"))
	command.AddCommand(newImageInspectCommand(logger, "inspect"))
	command.AddCommand(newImageImportCommand(logger))
	return command
}

func newImageListCommand(logger *slog.Logger, name string) *cobra.Command {
	var configPath string
	command := &cobra.Command{
		Use:   name,
		Short: "List locally imported OCI images",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			imageStore, _, err := configuredImageStore(configPath, logger)
			if err != nil {
				return err
			}
			images, err := imageStore.List(cmd.Context())
			if err != nil {
				return fmt.Errorf("list local OCI images: %w", err)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "REFERENCE\tOS/ARCH\tLAYERS\tMANIFEST DIGEST"); err != nil {
				return fmt.Errorf("write image table header: %w", err)
			}
			for _, item := range images {
				if _, err := fmt.Fprintf(writer, "%s\t%s/%s\t%d\t%s\n", item.Reference, item.OS, item.Architecture, len(item.Layers), shortDigest(item.ManifestDigest)); err != nil {
					return fmt.Errorf("write image table row: %w", err)
				}
			}
			if err := writer.Flush(); err != nil {
				return fmt.Errorf("flush image table: %w", err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&configPath, "config", "", "YAML runtime configuration path")
	return command
}

func newImageInspectCommand(logger *slog.Logger, name string) *cobra.Command {
	var configPath string
	command := &cobra.Command{
		Use:   name + " IMAGE",
		Short: "Inspect a locally imported OCI image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			imageStore, parser, err := configuredImageStore(configPath, logger)
			if err != nil {
				return err
			}
			parsed, err := parser.Parse(args[0])
			if err != nil {
				return fmt.Errorf("parse image reference: %w", err)
			}
			item, err := imageStore.Load(cmd.Context(), parsed)
			if err != nil {
				return fmt.Errorf("inspect image %q: %w", parsed.String(), err)
			}
			return writeImageJSON(cmd.OutOrStdout(), item)
		},
	}
	command.Flags().StringVar(&configPath, "config", "", "YAML runtime configuration path")
	return command
}

func newImageImportCommand(logger *slog.Logger) *cobra.Command {
	var configPath string
	command := &cobra.Command{
		Use:   "import REFERENCE OCI_LAYOUT",
		Short: "Import an existing local OCI image layout",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			imageStore, parser, err := configuredImageStore(configPath, logger)
			if err != nil {
				return err
			}
			parsed, err := parser.Parse(args[0])
			if err != nil {
				return fmt.Errorf("parse image reference: %w", err)
			}
			item, err := imageStore.Create(cmd.Context(), parsed, args[1])
			if err != nil {
				return fmt.Errorf("import local OCI image %q: %w", parsed.String(), err)
			}
			return writeImageJSON(cmd.OutOrStdout(), item)
		},
	}
	command.Flags().StringVar(&configPath, "config", "", "YAML runtime configuration path")
	return command
}

func configuredImageStore(configPath string, logger *slog.Logger) (image.ImageStore, image.ReferenceParser, error) {
	runtimeConfig := config.Defaults()
	if configPath != "" {
		loaded, err := config.Load(configPath)
		if err != nil {
			return nil, nil, fmt.Errorf("load runtime configuration: %w", err)
		}
		runtimeConfig = loaded
	}
	imageStore, err := store.NewLocal(runtimeConfig.ImagePath, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("configure local OCI image store: %w", err)
	}
	return imageStore, reference.NewParser(), nil
}

func writeImageJSON(writer io.Writer, item image.Image) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(item); err != nil {
		return fmt.Errorf("write image metadata: %w", err)
	}
	return nil
}

func shortDigest(digest string) string {
	algorithm, encoded, ok := strings.Cut(digest, ":")
	if !ok {
		return digest
	}
	if len(encoded) > 12 {
		encoded = encoded[:12]
	}
	return algorithm + ":" + encoded
}
