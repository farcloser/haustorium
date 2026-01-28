package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/farcloser/haustorium/version"
)

func main() {
	ctx := context.Background()

	appl := &cli.Command{
		Name:    version.Name(),
		Usage:   "Audio quality analysis tool",
		Version: version.Version() + " " + version.Commit(),
		Commands: []*cli.Command{
			analyzeCommand(),
			processCommand(),
		},
	}

	if err := appl.Run(ctx, os.Args); err != nil {
		slog.Error("failed to run", "error", err)
		os.Exit(1)
	}
}
