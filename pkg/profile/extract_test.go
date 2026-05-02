package profile_test

import (
	"reflect"
	"testing"

	"github.com/thingzio/devtrace/pkg/profile"
)

func TestClassifyAndExtract(t *testing.T) {
	cases := []struct {
		name           string
		bio            string
		blog           string
		wantPlatforms  []string // platform names in expected sort order
		wantEmailCount int
	}{
		{
			name:          "twitter and personal site",
			bio:           "Hi! Find me at https://x.com/janedoe and https://example.com",
			blog:          "",
			wantPlatforms: []string{"personal_site", "twitter"},
		},
		{
			name:          "blog field with bare host fills personal_site",
			bio:           "",
			blog:          "example.dev",
			wantPlatforms: []string{"personal_site"},
		},
		{
			name:          "blog with scheme is not double-prefixed",
			bio:           "",
			blog:          "https://example.dev",
			wantPlatforms: []string{"personal_site"},
		},
		{
			name:          "linkedin recognized",
			bio:           "linkedin: https://www.linkedin.com/in/jane-doe",
			wantPlatforms: []string{"linkedin"},
		},
		{
			name:          "mastodon distinct from blog",
			bio:           "https://hachyderm.io/@jane and https://blog.jane.io/posts/2025/01/01",
			wantPlatforms: []string{"mastodon", "unknown"},
		},
		{
			name:           "emails extracted and sorted",
			bio:            "reach me at JANE@example.com or jane@work.example.com",
			wantEmailCount: 2,
		},
		{
			name:           "regex email rejected by mail.ParseAddress",
			bio:            "version 1.2@3.4 not an email; real@example.com is",
			wantEmailCount: 1,
		},
		{
			name:          "duplicate URLs across bio and blog folded",
			bio:           "https://example.dev",
			blog:          "https://example.dev",
			wantPlatforms: []string{"personal_site"},
		},
		{
			name:          "trailing punctuation stripped",
			bio:           "Visit https://example.dev, my site.",
			wantPlatforms: []string{"personal_site"},
		},
		{
			name:          "x.com and twitter.com both classify as twitter",
			bio:           "https://twitter.com/a https://x.com/b",
			wantPlatforms: []string{"twitter", "twitter"},
		},
		{
			name:          "github.com profile URL classified",
			bio:           "old account: https://github.com/janedoe",
			wantPlatforms: []string{"github"},
		},
		{
			name:          "stackoverflow with id",
			bio:           "https://stackoverflow.com/users/12345/jane-doe",
			wantPlatforms: []string{"stackoverflow"},
		},
		{
			name: "empty inputs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accts, emails := profile.Extract(tc.bio, tc.blog)
			gotPlatforms := make([]string, len(accts))
			for i, a := range accts {
				gotPlatforms[i] = a.Platform
				if a.Tier != "T4" {
					t.Errorf("account %d: tier = %q, want T4", i, a.Tier)
				}
				if a.Source != "bio" && a.Source != "blog" {
					t.Errorf("account %d: source = %q, want bio or blog", i, a.Source)
				}
			}
			if !reflect.DeepEqual(gotPlatforms, tc.wantPlatforms) && (len(gotPlatforms) > 0 || len(tc.wantPlatforms) > 0) {
				t.Errorf("platforms: got %v, want %v", gotPlatforms, tc.wantPlatforms)
			}
			if len(emails) != tc.wantEmailCount {
				t.Errorf("emails: got %d, want %d (%v)", len(emails), tc.wantEmailCount, emails)
			}
		})
	}
}

func TestEmailsLowercaseAndDeduped(t *testing.T) {
	_, emails := profile.Extract("Foo@Example.com and FOO@example.com same address", "")
	if len(emails) != 1 {
		t.Fatalf("expected 1 deduped email, got %d: %v", len(emails), emails)
	}
	if emails[0] != "foo@example.com" {
		t.Errorf("got %q, want lowercased foo@example.com", emails[0])
	}
}
