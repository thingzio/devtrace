package logging

import (
	"log/slog"
	"os"

	"github.com/thingzio/devtrace/pkg/config"
)

func SetupLogger() {
	level := slog.LevelInfo
	if config.DebugEnabled() {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}
