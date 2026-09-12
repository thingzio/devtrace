// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"testing"
	"time"
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

func TestGetEnvAsDuration(t *testing.T) {
	t.Run("valid duration", func(t *testing.T) {
		t.Setenv("TEST_DUR", "12h30m")
		if got := GetEnvAsDuration("TEST_DUR", time.Hour); got != 12*time.Hour+30*time.Minute {
			t.Errorf("got %v, want 12h30m", got)
		}
	})

	t.Run("missing returns fallback", func(t *testing.T) {
		os.Unsetenv("TEST_DUR_MISSING")
		if got := GetEnvAsDuration("TEST_DUR_MISSING", 5*time.Second); got != 5*time.Second {
			t.Errorf("got %v, want 5s fallback", got)
		}
	})

	t.Run("invalid returns fallback", func(t *testing.T) {
		t.Setenv("TEST_DUR_BAD", "notaduration")
		if got := GetEnvAsDuration("TEST_DUR_BAD", time.Minute); got != time.Minute {
			t.Errorf("got %v, want 1m fallback", got)
		}
	})
}

func TestRepoSummaryTTLOverride(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		os.Unsetenv("DEVTRACE_REPO_SUMMARY_TTL")
		if got := RepoSummaryTTL(); got != 24*time.Hour {
			t.Errorf("got %v, want 24h default", got)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("DEVTRACE_REPO_SUMMARY_TTL", "6h")
		if got := RepoSummaryTTL(); got != 6*time.Hour {
			t.Errorf("got %v, want 6h from env", got)
		}
	})
}

func TestRepoListLimitOverride(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		os.Unsetenv("DEVTRACE_REPO_LIST_LIMIT")
		if got := RepoListLimit(); got != 300 {
			t.Errorf("got %d, want 300 default", got)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("DEVTRACE_REPO_LIST_LIMIT", "100")
		if got := RepoListLimit(); got != 100 {
			t.Errorf("got %d, want 100 from env", got)
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
