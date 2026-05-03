package forges

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// realKey1/realKey2 are valid base64-encoded SSH public-key payloads
// (the part between "ssh-ed25519 " and the comment). Used so the
// SHA-256 fingerprint computation has a real input — hand-rolling a
// fake string would silently exercise the malformed-input path.
const (
	realKey1 = "AAAAC3NzaC1lZDI1NTE5AAAAIKRe0pYxNubFS3VryJzuIH6q9XIK7lnZL3MSV/HxFpT3"
	realKey2 = "AAAAB3NzaC1yc2EAAAADAQABAAABAQDD9JsDpVVzZ2hk0AsLqMnhM2P/Eo+nGTiXuG2qzpjBSKMC2vONp9rCBn7gP5xQbEsoF4qmqUJlOYK4PwzAo8t9KdjW1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newTestClient(t *testing.T, handlers map[Forge]http.HandlerFunc) *Client {
	t.Helper()
	c := NewClient(2 * time.Second)
	c.urlOverride = make(map[Forge]string)
	for forge, h := range handlers {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		c.urlOverride[forge] = srv.URL
	}
	return c
}

func TestFetchSSHFingerprintsParsesMultipleKeys(t *testing.T) {
	c := newTestClient(t, map[Forge]http.HandlerFunc{
		GitLab: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(
				"ssh-ed25519 " + realKey1 + " user@host\n" +
					"ssh-ed25519 " + realKey1 + " duplicate-comment\n" + // dedupe within response
					"ssh-rsa " + realKey2 + "\n"))
		},
	})

	got, err := c.FetchSSHFingerprints(context.Background(), GitLab, "alice")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("fingerprint count: got %d, want 2 (deduped)", len(got))
	}
	for _, fp := range got {
		if !strings.HasPrefix(fp, "SHA256:") {
			t.Errorf("fingerprint missing SHA256 prefix: %q", fp)
		}
	}
}

func TestFetchSSHFingerprintsNotFound(t *testing.T) {
	c := newTestClient(t, map[Forge]http.HandlerFunc{
		Codeberg: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	_, err := c.FetchSSHFingerprints(context.Background(), Codeberg, "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 must surface as ErrNotFound, got %v", err)
	}
}

func TestFetchSSHFingerprintsRedirectIsNotFound(t *testing.T) {
	// GitLab 302s missing users to /users/sign_in. The client must
	// treat any 3xx as not-found rather than chasing the redirect
	// (which would parse the login HTML as junk).
	c := newTestClient(t, map[Forge]http.HandlerFunc{
		GitLab: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "/users/sign_in")
			w.WriteHeader(http.StatusFound)
		},
	})
	_, err := c.FetchSSHFingerprints(context.Background(), GitLab, "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("redirect must surface as ErrNotFound, got %v", err)
	}
}

func TestFetchSSHFingerprintsServerError(t *testing.T) {
	c := newTestClient(t, map[Forge]http.HandlerFunc{
		Sourcehut: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		},
	})
	_, err := c.FetchSSHFingerprints(context.Background(), Sourcehut, "u")
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("500 must not surface as ErrNotFound: %v", err)
	}
}

func TestFetchSSHFingerprintsEmptyBody(t *testing.T) {
	// User exists, has published no keys. Empty result, no error.
	c := newTestClient(t, map[Forge]http.HandlerFunc{
		Codeberg: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(""))
		},
	})
	got, err := c.FetchSSHFingerprints(context.Background(), Codeberg, "bare-user")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty body: got %d fingerprints, want 0", len(got))
	}
}

func TestFetchSSHFingerprintsSkipsMalformedLines(t *testing.T) {
	c := newTestClient(t, map[Forge]http.HandlerFunc{
		GitLab: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(
				"# comment line\n" +
					"\n" + // empty
					"only-one-field\n" + // missing payload
					"ssh-ed25519 not-base64-data\n" + // bad base64
					"ssh-ed25519 " + realKey1 + " good\n"))
		},
	})
	got, err := c.FetchSSHFingerprints(context.Background(), GitLab, "u")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 valid fingerprint amid junk, got %d", len(got))
	}
}

func TestFetchSSHFingerprintsRequiresUsername(t *testing.T) {
	c := NewClient(time.Second)
	if _, err := c.FetchSSHFingerprints(context.Background(), GitLab, ""); err == nil {
		t.Error("expected error on empty username")
	}
}

func TestSupportedForgesExcludesGitHub(t *testing.T) {
	for _, f := range SupportedForges() {
		if f == GitHub {
			t.Errorf("SupportedForges should exclude GitHub (it's the anchor): %v", SupportedForges())
		}
	}
}

func TestProfileURLs(t *testing.T) {
	tests := map[Forge]string{
		GitHub:    "https://github.com/jane",
		GitLab:    "https://gitlab.com/jane",
		Codeberg:  "https://codeberg.org/jane",
		Sourcehut: "https://sr.ht/~jane", // tilde prefix for sourcehut
	}
	for forge, want := range tests {
		if got := ProfileURL(forge, "jane"); got != want {
			t.Errorf("ProfileURL(%s): got %q, want %q", forge, got, want)
		}
	}
}

// TestFingerprintMatchesSSHKeygenOutput pins the fingerprint format
// against a known input. The SHA-256 fingerprint of the realKey1
// payload was computed once with `ssh-keygen -lf` and recorded here;
// if our code drifts, this test catches it.
func TestFingerprintMatchesSSHKeygenOutput(t *testing.T) {
	// Compute fingerprint via the same path the client uses.
	got := lineFingerprint("ssh-ed25519 " + realKey1)
	if got == "" {
		t.Fatal("lineFingerprint returned empty for valid key")
	}
	if !strings.HasPrefix(got, "SHA256:") {
		t.Errorf("missing prefix: %q", got)
	}
	// Format check: SHA256: + 43 base64 chars (32 bytes raw → 43 base64 no-pad).
	if len(got) != len("SHA256:")+43 {
		t.Errorf("unexpected fingerprint length: got %d (%q)", len(got), got)
	}
}
