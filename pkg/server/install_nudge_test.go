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

package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// captureLogs redirects the default logger for the duration of a test.
// Tests using it must not run in parallel: slog.SetDefault is global.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// The nudge fires only on sign-in, so counting impressions is the only way to
// tell "saw it and declined" apart from "never came back". Without this the
// install rate has no denominator.
func TestPostAuthRedirectRecordsNudgeImpression(t *testing.T) {
	logs := captureLogs(t)

	got := postAuthRedirect(false, "octocat")

	if got != "/settings?msg=install_app" {
		t.Errorf("redirect = %q, want /settings?msg=install_app", got)
	}
	out := logs.String()
	if !strings.Contains(out, "install nudge shown") {
		t.Errorf("no impression logged; got %q", out)
	}
	if !strings.Contains(out, "octocat") {
		t.Errorf("impression missing username; got %q", out)
	}
}

func TestPostAuthRedirectSendsInstalledTenantToDashboard(t *testing.T) {
	logs := captureLogs(t)

	got := postAuthRedirect(true, "octocat")

	if got != "/dashboard" {
		t.Errorf("redirect = %q, want /dashboard", got)
	}
	if strings.Contains(logs.String(), "install nudge shown") {
		t.Error("logged an impression for a tenant that already installed")
	}
}
