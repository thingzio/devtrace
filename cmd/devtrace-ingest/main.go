package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/lib/pq"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/ingest"
	"github.com/thingzio/devtrace/pkg/logging"
)

var (
	version = "v0.0.1-default"
	commit  = ""
	date    = ""
)

func main() {
	logging.SetupLogger()
	slog.Info("starting devtrace-ingest", "version", version, "commit", commit, "date", date)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := postgres.NewFromEnv(ctx)
	if err != nil {
		slog.Error("init store", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		slog.Error("migrate", "error", err)
		os.Exit(1)
	}

	if err := ingest.Run(ctx, store); err != nil {
		slog.Error("ingest failed", "error", err)
		os.Exit(1)
	}

	slog.Info("ingest complete")
}
