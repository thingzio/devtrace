package server

import (
	"bytes"
	"strings"
	"testing"
)

func renderTOS(t *testing.T, opts Options) string {
	t.Helper()
	serverOpts = opts
	t.Cleanup(func() { serverOpts = Options{} })

	tmpl, ok := pageTemplates["tos.html"]
	if !ok {
		t.Fatal("tos.html template not registered")
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", pageData{Title: "Terms"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestFooterLinks(t *testing.T) {
	body := renderTOS(t, Options{Version: "v1.2.3", Commit: "abc1234"})

	for _, want := range []string{
		`href="/help">HELP<`,
		`href="/tos">TERMS<`,
		`href="https://github.com/thingzio/devtrace"`,
		"VERSION v1.2.3",
		"(abc1234)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("footer missing %q", want)
		}
	}
	if strings.Contains(body, "CHANGELOG") {
		t.Error("footer still links the changelog")
	}
}

func TestFooterOmitsVersionWhenUnset(t *testing.T) {
	body := renderTOS(t, Options{})

	if strings.Contains(body, "VERSION") {
		t.Error("footer rendered a version label with no version")
	}
	if !strings.Contains(body, `href="/help">HELP<`) {
		t.Error("footer lost its links when the version was absent")
	}
}

func TestHomeUsesSameFooter(t *testing.T) {
	serverOpts = Options{Version: "v1.2.3", Commit: "abc1234"}
	t.Cleanup(func() { serverOpts = Options{} })

	tmpl := pageTemplates["home.html"]
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "sitefooter", nil); err != nil {
		t.Fatalf("render sitefooter: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "VERSION v1.2.3") || strings.Contains(body, "CHANGELOG") {
		t.Errorf("home footer diverged: %s", body)
	}
}
