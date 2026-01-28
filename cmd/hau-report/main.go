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
		Usage:   "Generate and analyze haustorium quality reports",
		Version: version.Version() + " " + version.Commit(),
		Commands: []*cli.Command{
			reportCommand(),
			digestCommand(),
		},
	}

	if err := appl.Run(ctx, os.Args); err != nil {
		slog.Error("failed to run", "error", err)
		os.Exit(1)
	}
}
