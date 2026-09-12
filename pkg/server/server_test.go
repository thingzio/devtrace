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
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getTestServer(t *testing.T, handler http.Handler) *http.Response {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	return resp
}

func TestSecurityHeaders(t *testing.T) {
	t.Setenv("BASE_URL", "")

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp := getTestServer(t, securityHeaders(inner))
	defer resp.Body.Close()

	exact := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for h, want := range exact {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}

	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "default-src 'self'") {
		t.Errorf("CSP should start with \"default-src 'self'\", got %q", csp)
	}

	if pp := resp.Header.Get("Permissions-Policy"); pp == "" {
		t.Error("Permissions-Policy header missing")
	}
}

func TestSecurityHeadersNoHSTSWithoutHTTPS(t *testing.T) {
	t.Setenv("BASE_URL", "")

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp := getTestServer(t, securityHeaders(inner))
	defer resp.Body.Close()

	if hsts := resp.Header.Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("HSTS should not be set without HTTPS, got %q", hsts)
	}
}

func TestSecurityHeadersNoHSTSWithHTTP(t *testing.T) {
	t.Setenv("BASE_URL", "http://localhost:8080")

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp := getTestServer(t, securityHeaders(inner))
	defer resp.Body.Close()

	if hsts := resp.Header.Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("HSTS should not be set for http BASE_URL, got %q", hsts)
	}
}
