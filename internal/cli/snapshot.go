package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vellora/vellora/internal/config"
	"github.com/vellora/vellora/internal/storage/snapshot"
)

func newSnapshotCommand(logger *slog.Logger) *cobra.Command {
	command := &cobra.Command{Use: "snapshot", Short: "Manage local filesystem snapshots"}
	command.AddCommand(newSnapshotListCommand(logger), newSnapshotInspectCommand(logger), newSnapshotRemoveCommand(logger))
	return command
}

func snapshotManager(configPath string, logger *slog.Logger) (snapshot.Snapshotter, error) {
	cfg := config.Defaults()
	if configPath != "" {
		loaded, err := config.Load(configPath)
		if err != nil {
			return nil, fmt.Errorf("load runtime configuration: %w", err)
		}
		cfg = loaded
	}
	return snapshot.New(cfg.RuntimeRoot, logger)
}

func newSnapshotListCommand(logger *slog.Logger) *cobra.Command {
	var configPath string
	command := &cobra.Command{Use: "ls", Short: "List snapshots", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		manager, err := snapshotManager(configPath, logger)
		if err != nil {
			return err
		}
		items, err := manager.List(cmd.Context())
		if err != nil {
			return fmt.Errorf("list snapshots: %w", err)
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(writer, "ID\tPARENT\tSTATUS\tREFS\tCONTAINER"); err != nil {
			return err
		}
		for _, item := range items {
			if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%s\n", item.ID, item.Parent, item.Status, item.ReferenceCount, item.ContainerID); err != nil {
				return err
			}
		}
		return writer.Flush()
	}}
	command.Flags().StringVar(&configPath, "config", "", "YAML runtime configuration path")
	return command
}

func newSnapshotInspectCommand(logger *slog.Logger) *cobra.Command {
	var configPath string
	command := &cobra.Command{Use: "inspect ID", Short: "Show snapshot metadata", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := snapshotManager(configPath, logger)
		if err != nil {
			return err
		}
		item, err := manager.Inspect(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("inspect snapshot %q: %w", args[0], err)
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(item); err != nil {
			return fmt.Errorf("write snapshot metadata: %w", err)
		}
		return nil
	}}
	command.Flags().StringVar(&configPath, "config", "", "YAML runtime configuration path")
	return command
}

func newSnapshotRemoveCommand(logger *slog.Logger) *cobra.Command {
	var configPath string
	command := &cobra.Command{Use: "rm ID", Short: "Delete an unreferenced snapshot", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := snapshotManager(configPath, logger)
		if err != nil {
			return err
		}
		if err := manager.Delete(cmd.Context(), args[0]); err != nil {
			return fmt.Errorf("remove snapshot %q: %w", args[0], err)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), args[0])
		return err
	}}
	command.Flags().StringVar(&configPath, "config", "", "YAML runtime configuration path")
	return command
}
