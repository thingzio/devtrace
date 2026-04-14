package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/thingzio/devtrace/pkg/logging"
	"github.com/thingzio/devtrace/pkg/server"
)

var (
	version = "v0.0.1-default"
	commit  = ""
	date    = ""
)

func main() {
	logging.SetupLogger()
	slog.Info("starting devtrace-site", "version", version, "commit", commit, "date", date)

	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, server.Options{Version: version, Commit: commit, Date: date}); err != nil {
		slog.Error("fatal error", "error", err)
		return 1
	}
	return 0
}
