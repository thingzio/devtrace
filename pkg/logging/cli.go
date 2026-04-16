package logging

import (
	"log/slog"
	"os"

	"github.com/thingzio/devtrace/pkg/config"
)

// SetupLogger configures the default slog logger with JSON output.
// If version is non-empty, it is included in every log entry automatically.
func SetupLogger(version string) {
	level := slog.LevelInfo
	if config.DebugEnabled() {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	if version != "" {
		logger = logger.With("version", version)
	}
	slog.SetDefault(logger)
}
