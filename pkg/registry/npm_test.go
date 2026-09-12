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

package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestNPMClient(t *testing.T, h http.HandlerFunc) *NPMClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewNPMClient(2 * time.Second)
	c.baseURL = srv.URL
	return c
}

func TestNPMFetchOK(t *testing.T) {
	c := newTestNPMClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/-/user/sindre/package" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"alpha": "write",
			"beta": "write",
			"gamma": "read",
			"delta": "write",
			"epsilon": "write",
			"zeta": "write",
			"reader-pkg": "read"
		}`))
	})

	total, top, err := c.FetchUserPackages(context.Background(), "sindre", 3)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if total != 5 {
		t.Errorf("total: got %d, want 5 (write-only filtered)", total)
	}
	if len(top) != 3 {
		t.Fatalf("top len: got %d, want 3", len(top))
	}
	// Alphabetical: alpha, beta, delta...
	wantNames := []string{"alpha", "beta", "delta"}
	for i, w := range wantNames {
		if top[i].Name != w {
			t.Errorf("top[%d].Name: got %q, want %q", i, top[i].Name, w)
		}
		if top[i].Role != "write" {
			t.Errorf("top[%d].Role: got %q, want write", i, top[i].Role)
		}
		if !strings.Contains(top[i].URL, "npmjs.com/package/") {
			t.Errorf("top[%d].URL: got %q", i, top[i].URL)
		}
	}
}

func TestNPMFetchNotFound(t *testing.T) {
	c := newTestNPMClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, _, err := c.FetchUserPackages(context.Background(), "ghost", 5)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestNPMFetchServerError(t *testing.T) {
	c := newTestNPMClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	_, _, err := c.FetchUserPackages(context.Background(), "u", 5)
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("500 must not surface as ErrNotFound: %v", err)
	}
}

func TestNPMFetchAllReaders(t *testing.T) {
	// User is listed as collaborator but holds write on no package.
	// Should return total=0 (publishes nothing under this handle).
	c := newTestNPMClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"a":"read","b":"read"}`))
	})
	total, top, err := c.FetchUserPackages(context.Background(), "u", 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if total != 0 {
		t.Errorf("total: got %d, want 0 (no write-role packages)", total)
	}
	if len(top) != 0 {
		t.Errorf("top should be empty, got %d", len(top))
	}
}

func TestNPMFetchTopCapping(t *testing.T) {
	// Simulate a prolific publisher: top limit caps the displayed list
	// without affecting the total count.
	c := newTestNPMClient(t, func(w http.ResponseWriter, _ *http.Request) {
		body := `{`
		for i := 0; i < 50; i++ {
			if i > 0 {
				body += ","
			}
			body += `"pkg-` + string(rune('a'+i%26)) + string(rune('0'+i/26)) + `":"write"`
		}
		body += `}`
		_, _ = w.Write([]byte(body))
	})
	total, top, err := c.FetchUserPackages(context.Background(), "prolific", 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if total != 50 {
		t.Errorf("total: got %d, want 50", total)
	}
	if len(top) != 5 {
		t.Errorf("top len: got %d, want 5 (capped)", len(top))
	}
}

func TestNPMFetchRequiresUsername(t *testing.T) {
	c := NewNPMClient(time.Second)
	if _, _, err := c.FetchUserPackages(context.Background(), "", 5); err == nil {
		t.Error("expected error on empty username")
	}
}
