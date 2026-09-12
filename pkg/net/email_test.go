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

package net

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendEmailCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := SendEmail(ctx, "key", "from@test.com", "to@test.com", "subject", "<p>html</p>", "text", "")
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestSendEmailSuccess(t *testing.T) {
	var gotReq struct {
		method      string
		contentType string
		authHeader  string
		body        map[string]any
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq.method = r.Method
		gotReq.contentType = r.Header.Get("Content-Type")
		gotReq.authHeader = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotReq.body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Temporarily replace the package-level client and URL.
	origClient := emailClient
	defer func() { emailClient = origClient }()
	emailClient = srv.Client()

	err := sendEmailTo(context.Background(), srv.URL, "test-api-key",
		"sender@example.com", "recipient@example.com",
		"Test Subject", "<p>hello</p>", "hello", "reply@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotReq.method != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotReq.method)
	}
	if gotReq.contentType != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", gotReq.contentType)
	}
	if gotReq.authHeader != "Bearer test-api-key" {
		t.Errorf("Authorization: got %q, want %q", gotReq.authHeader, "Bearer test-api-key")
	}
	if gotReq.body["from"] != "sender@example.com" {
		t.Errorf("from: got %v, want sender@example.com", gotReq.body["from"])
	}
	if gotReq.body["subject"] != "Test Subject" {
		t.Errorf("subject: got %v, want Test Subject", gotReq.body["subject"])
	}
	if gotReq.body["reply_to"] != "reply@example.com" {
		t.Errorf("reply_to: got %v, want reply@example.com", gotReq.body["reply_to"])
	}
}

func TestSendEmailNoReplyTo(t *testing.T) {
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origClient := emailClient
	defer func() { emailClient = origClient }()
	emailClient = srv.Client()

	err := sendEmailTo(context.Background(), srv.URL, "key",
		"from@test.com", "to@test.com", "subj", "<p>hi</p>", "hi", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := body["reply_to"]; ok {
		t.Error("reply_to should be omitted when empty")
	}
}

func TestSendEmailWithUnsubscribeURL(t *testing.T) {
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origClient := emailClient
	defer func() { emailClient = origClient }()
	emailClient = srv.Client()

	err := sendEmailTo(context.Background(), srv.URL, "key",
		"from@test.com", "to@test.com", "subj", "<p>hi</p>", "hi", "",
		WithUnsubscribeURL("https://example.com/settings"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hdrs, ok := body["headers"].(map[string]any)
	if !ok {
		t.Fatal("expected headers in payload")
	}
	if got := hdrs["List-Unsubscribe"]; got != "<https://example.com/settings>" {
		t.Errorf("List-Unsubscribe: got %v, want %v", got, "<https://example.com/settings>")
	}
	if got := hdrs["List-Unsubscribe-Post"]; got != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post: got %v, want %v", got, "List-Unsubscribe=One-Click")
	}
}

func TestSendEmailAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"rate limited"}`))
	}))
	defer srv.Close()

	origClient := emailClient
	defer func() { emailClient = origClient }()
	emailClient = srv.Client()

	err := sendEmailTo(context.Background(), srv.URL, "key",
		"from@test.com", "to@test.com", "subj", "<p>hi</p>", "hi", "")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	want := "email API returned 500"
	if got := err.Error(); len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("error: got %q, want prefix %q", got, want)
	}
}
