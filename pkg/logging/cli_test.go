package logging

import (
	"os"
	"testing"
)

func TestSetupLogger(t *testing.T) {
	t.Run("default level", func(t *testing.T) {
		os.Unsetenv("DEVTRACE_DEBUG")
		SetupLogger("v0.0.1-test") // must not panic
	})

	t.Run("debug level", func(t *testing.T) {
		t.Setenv("DEVTRACE_DEBUG", "true")
		SetupLogger("v0.0.1-test") // must not panic
	})

	t.Run("empty version", func(t *testing.T) {
		os.Unsetenv("DEVTRACE_DEBUG")
		SetupLogger("") // must not panic
	})
}
