package logging

import (
	"os"
	"testing"
)

func TestSetupLogger(t *testing.T) {
	t.Run("default level", func(t *testing.T) {
		os.Unsetenv("DEVTRACE_DEBUG")
		SetupLogger() // must not panic
	})

	t.Run("debug level", func(t *testing.T) {
		t.Setenv("DEVTRACE_DEBUG", "true")
		SetupLogger() // must not panic
	})
}
