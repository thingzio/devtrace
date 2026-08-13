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
