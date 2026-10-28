package main

import (
	"log/slog"
	"os"

	"github.com/vellora/vellora/internal/cli"
	"github.com/vellora/vellora/internal/logger"
)

func main() {
	if err := cli.NewRootCommand(logger.New(os.Stderr)).Execute(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}
