package server

import (
	"regexp"
	"testing"
)

// TestTemplateConstKeysAreCanonical asserts the const VALUES in
// template_keys.go are the strings we actually want — guards against
// silent typos in the const declarations themselves.
func TestTemplateConstKeysAreCanonical(t *testing.T) {
	checks := map[string]string{
		"tmplTitle":            "Title",
		"tmplVersion":          "Version",
		"tmplCommit":           "Commit",
		"tmplDate":             "Date",
		"tmplAdmin":            "Admin",
		"tmplHelp":             "Help",
		"tmplName":             "name",
		"tmplPlanFree":         "free",
		"tmplPlanPro":          "pro",
		"tmplErrorKey":         "error",
		"tmplNavUser":          "NavUser",
		"tmplNavAvatar":        "NavAvatar",
		"msgRateLimitExceeded": "rate limit exceeded",
	}
	got := map[string]string{
		"tmplTitle":            tmplTitle,
		"tmplVersion":          tmplVersion,
		"tmplCommit":           tmplCommit,
		"tmplDate":             tmplDate,
		"tmplAdmin":            tmplAdmin,
		"tmplHelp":             tmplHelp,
		"tmplName":             tmplName,
		"tmplPlanFree":         tmplPlanFree,
		"tmplPlanPro":          tmplPlanPro,
		"tmplErrorKey":         tmplErrorKey,
		"tmplNavUser":          tmplNavUser,
		"tmplNavAvatar":        tmplNavAvatar,
		"msgRateLimitExceeded": msgRateLimitExceeded,
	}
	for name, want := range checks {
		if got[name] != want {
			t.Errorf("%s: got %q, want %q", name, got[name], want)
		}
	}
}

// TestLayoutFieldsCoveredByConsts verifies every top-level field
// reference in layout.html is satisfied by a template_keys const or by
// a known dynamic field (NavUser, NavAvatar, etc.). Catches the
// regression class where a const value drifts from the template's
// `{{.Field}}` reference, leaving fields silently empty.
func TestLayoutFieldsCoveredByConsts(t *testing.T) {
	layoutBytes, err := templateFS.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("read layout.html: %v", err)
	}

	// Match top-level field references: {{.Title}}, {{ .Version }}, etc.
	// Excludes nested references like {{.User.Name}} which are not
	// surfaced through template_keys.go.
	fieldRE := regexp.MustCompile(`\{\{[\s-]*\.([A-Z][A-Za-z0-9]*)\b`)
	matches := fieldRE.FindAllStringSubmatch(string(layoutBytes), -1)

	referenced := make(map[string]bool, len(matches))
	for _, m := range matches {
		referenced[m[1]] = true
	}
	if len(referenced) == 0 {
		t.Fatal("found no field references in layout.html — regex likely broken")
	}

	// Fields satisfied by template_keys.go const VALUES.
	covered := map[string]bool{
		tmplTitle:   true,
		tmplVersion: true,
		tmplCommit:  true,
		tmplDate:    true,
		tmplAdmin:   true,
		tmplHelp:    true,
	}
	// Layout-context fields covered by template_keys.go consts but
	// listed here separately for clarity since they're set by handlers
	// dynamically rather than being product/UI labels.
	allowList := map[string]bool{
		tmplNavUser:   true,
		tmplNavAvatar: true,
	}

	for field := range referenced {
		if covered[field] || allowList[field] {
			continue
		}
		t.Errorf("layout.html references {{.%s}} but no template_keys.go "+
			"const has that value and it isn't in the allow-list. "+
			"Either add a const, add it to allowList, or fix the typo.",
			field)
	}
}

// TestRoutesUsingErrorEnvelope is a placeholder marker — real tests
// covering the writeError JSON shape live in handler_*_test.go and
// rely on the same map[string]string{tmplErrorKey: msg} contract.
func TestRoutesUsingErrorEnvelope(t *testing.T) {
	if tmplErrorKey != "error" {
		t.Errorf("API contract: error envelope key changed unexpectedly: %q", tmplErrorKey)
	}
}
