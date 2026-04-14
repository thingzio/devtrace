package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
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

	if err := run(); err != nil {
		log.Fatal(err)
	}

	slog.Info("ingest complete")
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := postgres.NewFromEnv(ctx)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	return ingest.Run(ctx, store)
}
