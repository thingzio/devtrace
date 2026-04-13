package config

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	t.Run("returns value when set", func(t *testing.T) {
		t.Setenv("TEST_GET_ENV", "hello")
		if got := GetEnv("TEST_GET_ENV", "default"); got != "hello" {
			t.Errorf("GetEnv() = %q, want %q", got, "hello")
		}
	})

	t.Run("returns fallback when missing", func(t *testing.T) {
		os.Unsetenv("TEST_GET_ENV_MISSING")
		if got := GetEnv("TEST_GET_ENV_MISSING", "fallback"); got != "fallback" {
			t.Errorf("GetEnv() = %q, want %q", got, "fallback")
		}
	})
}

func TestGetEnvAsInt(t *testing.T) {
	t.Run("valid int", func(t *testing.T) {
		t.Setenv("TEST_INT", "42")
		if got := GetEnvAsInt("TEST_INT", 10); got != 42 {
			t.Errorf("GetEnvAsInt() = %d, want %d", got, 42)
		}
	})

	t.Run("missing returns fallback", func(t *testing.T) {
		os.Unsetenv("TEST_INT_MISSING")
		if got := GetEnvAsInt("TEST_INT_MISSING", 10); got != 10 {
			t.Errorf("GetEnvAsInt() = %d, want %d", got, 10)
		}
	})

	t.Run("invalid string returns fallback", func(t *testing.T) {
		t.Setenv("TEST_INT_BAD", "notanint")
		if got := GetEnvAsInt("TEST_INT_BAD", 10); got != 10 {
			t.Errorf("GetEnvAsInt() = %d, want %d", got, 10)
		}
	})

	t.Run("zero or negative returns fallback", func(t *testing.T) {
		t.Setenv("TEST_INT_ZERO", "0")
		if got := GetEnvAsInt("TEST_INT_ZERO", 5); got != 5 {
			t.Errorf("GetEnvAsInt() = %d, want %d", got, 5)
		}
	})
}

func TestGetEnvBool(t *testing.T) {
	t.Run("true string", func(t *testing.T) {
		t.Setenv("TEST_BOOL", "true")
		if got := GetEnvBool("TEST_BOOL"); !got {
			t.Error("GetEnvBool() = false, want true")
		}
	})

	t.Run("1 string", func(t *testing.T) {
		t.Setenv("TEST_BOOL", "1")
		if got := GetEnvBool("TEST_BOOL"); !got {
			t.Error("GetEnvBool() = false, want true")
		}
	})

	t.Run("TRUE uppercase", func(t *testing.T) {
		t.Setenv("TEST_BOOL", "TRUE")
		if got := GetEnvBool("TEST_BOOL"); !got {
			t.Error("GetEnvBool() = false, want true")
		}
	})

	t.Run("missing returns false", func(t *testing.T) {
		os.Unsetenv("TEST_BOOL_MISSING")
		if got := GetEnvBool("TEST_BOOL_MISSING"); got {
			t.Error("GetEnvBool() = true, want false")
		}
	})
}

func TestGetEnvAsFloat(t *testing.T) {
	t.Run("valid float", func(t *testing.T) {
		t.Setenv("TEST_FLOAT", "3.14")
		if got := GetEnvAsFloat("TEST_FLOAT", 1.0); got != 3.14 {
			t.Errorf("GetEnvAsFloat() = %f, want %f", got, 3.14)
		}
	})

	t.Run("missing returns fallback", func(t *testing.T) {
		os.Unsetenv("TEST_FLOAT_MISSING")
		if got := GetEnvAsFloat("TEST_FLOAT_MISSING", 2.71); got != 2.71 {
			t.Errorf("GetEnvAsFloat() = %f, want %f", got, 2.71)
		}
	})

	t.Run("invalid returns fallback", func(t *testing.T) {
		t.Setenv("TEST_FLOAT_BAD", "notafloat")
		if got := GetEnvAsFloat("TEST_FLOAT_BAD", 2.71); got != 2.71 {
			t.Errorf("GetEnvAsFloat() = %f, want %f", got, 2.71)
		}
	})
}

func TestDebugEnabled(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		t.Setenv("DEVTRACE_DEBUG", "true")
		if !DebugEnabled() {
			t.Error("DebugEnabled() = false, want true")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		os.Unsetenv("DEVTRACE_DEBUG")
		if DebugEnabled() {
			t.Error("DebugEnabled() = true, want false")
		}
	})
}
