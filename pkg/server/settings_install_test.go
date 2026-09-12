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
	"net/http/httptest"
	"strings"
	"testing"
)

// The install nudge redirects to /settings?msg=install_app, so the code has to
// resolve to prose. Echoing the raw parameter shipped the literal string
// "install_app" to every user who had not installed the app.
func TestFlashMessageResolvesKnownCode(t *testing.T) {
	t.Parallel()

	got := flashMessage("install_app")

	if got == "" {
		t.Fatal("flashMessage(install_app): got empty, want prose")
	}
	if got == "install_app" {
		t.Error("flashMessage(install_app): got the raw code, want prose")
	}
	if !strings.Contains(strings.ToLower(got), "github app") {
		t.Errorf("flashMessage(install_app) = %q, want it to mention the GitHub App", got)
	}
}

// ?msg= is attacker-controllable, so anything unrecognized must be dropped
// rather than rendered. html/template escapes it, so this is not XSS — but
// echoing it lets a crafted link put arbitrary prose on a victim's settings
// page, which is a phishing surface.
func TestFlashMessageDropsUnknownCodes(t *testing.T) {
	t.Parallel()

	for _, code := range []string{
		"Your account is suspended. Call 1-800-555-0100.",
		"bogus",
		"",
	} {
		if got := flashMessage(code); got != "" {
			t.Errorf("flashMessage(%q) = %q, want empty", code, got)
		}
	}
}

func renderSettings(t *testing.T, data map[string]any) string {
	t.Helper()
	rec := httptest.NewRecorder()
	renderTemplate(rec, "settings.html", data)
	if rec.Code != 200 {
		t.Fatalf("render failed: status %d", rec.Code)
	}
	return rec.Body.String()
}

func settingsData(hasInstall bool) map[string]any {
	return map[string]any{
		tmplTitle:          "Settings",
		tmplVersion:        "v1.0.0",
		tmplNavUser:        "octocat",
		tmplKeyUsername:    "octocat",
		tmplKeyPlan:        "pro",
		"max_contributors": 1000,
		tmplKeyRateLimit:   500,
		"max_watchlists":   3,
		"has_install":      hasInstall,
		"app_install_url":  "https://github.com/apps/DevTraceThingz/installations/new",
	}
}

// Most tenants are on corporate accounts where an org install needs owner
// approval. A personal-account install contributes the same 30 search
// req/min, so the page has to say so instead of implying the org path.
func TestSettingsInstallSectionLeadsWithPersonalAccount(t *testing.T) {
	t.Parallel()

	body := renderSettings(t, settingsData(false))

	for _, want := range []string{
		"personal account",
		"No admin approval",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("install section missing %q", want)
		}
	}
}

// These are infrastructure and Kubernetes maintainers; they read permission
// lists. Each scope has to be named with the reason it is requested.
func TestSettingsInstallSectionExplainsEachPermission(t *testing.T) {
	t.Parallel()

	body := renderSettings(t, settingsData(false))

	for _, want := range []string{"Metadata", "Contents", "Pull requests", "Members"} {
		if !strings.Contains(body, want) {
			t.Errorf("install section missing permission %q", want)
		}
	}
	if !strings.Contains(strings.ToLower(body), "read-only") {
		t.Error("install section should state the permissions are read-only")
	}
}

// The old copy claimed the install enabled scoring, which already works
// without it. Overstating the benefit is what made the ask ignorable.
func TestSettingsInstallSectionDropsFalseScoringClaim(t *testing.T) {
	t.Parallel()

	body := renderSettings(t, settingsData(false))

	if strings.Contains(body, "enable contributor scoring for your repositories") {
		t.Error("install section still claims install enables scoring; scoring works without it")
	}
}

func TestSettingsHidesInstallSectionOnceInstalled(t *testing.T) {
	t.Parallel()

	body := renderSettings(t, settingsData(true))

	if strings.Contains(body, "Install GitHub App") {
		t.Error("install CTA rendered for a tenant that already installed")
	}
}
