package cli

import (
	"log/slog"

	"github.com/spf13/cobra"
)

// NewRootCommand builds the CLI without starting runtime work during construction.
func NewRootCommand(logger *slog.Logger) *cobra.Command {
	root := &cobra.Command{
		Use:           "vellora",
		Short:         "An educational OCI-inspired container runtime",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newRunCommand(logger))
	root.AddCommand(newInitCommand(logger))
	root.AddCommand(newImageListCommand(logger, "images"))
	root.AddCommand(newImageInspectCommand(logger, "inspect"))
	root.AddCommand(newImageCommand(logger))
	root.AddCommand(newSnapshotCommand(logger))
	root.AddCommand(newNetworkCommand(logger))
	root.AddCommand(newPullCommand(logger))
	for _, name := range []string{"exec", "ps", "stop", "kill", "rm", "logs", "stats"} {
		root.AddCommand(newPlaceholderCommand(name, logger))
	}
	return root
}

func newPlaceholderCommand(name string, logger *slog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: "Reserved for the " + name + " workflow",
		RunE: func(_ *cobra.Command, _ []string) error {
			logger.Info("command is scaffolded for a future milestone", "command", name)
			return nil
		},
	}
}
