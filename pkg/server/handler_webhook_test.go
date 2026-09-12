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
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func computeHMAC(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"action":"created"}`)

	t.Run("valid", func(t *testing.T) {
		sig := computeHMAC(payload, secret)
		if !verifySignature(payload, sig, secret) {
			t.Error("expected valid signature")
		}
	})

	t.Run("invalid_secret", func(t *testing.T) {
		sig := computeHMAC(payload, "wrong-secret")
		if verifySignature(payload, sig, secret) {
			t.Error("expected invalid signature")
		}
	})

	t.Run("missing_prefix", func(t *testing.T) {
		if verifySignature(payload, "nope", secret) {
			t.Error("expected false for missing prefix")
		}
	})

	t.Run("bad_hex", func(t *testing.T) {
		if verifySignature(payload, "sha256=zzzz", secret) {
			t.Error("expected false for bad hex")
		}
	})
}

func TestWebhookHandlerInvalidSignature(t *testing.T) {
	handler := webhookHandler(nil, nil, "secret", nil)

	body := []byte(`{}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=bad")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleInstallationEvent_BadJSON(t *testing.T) {
	err := handleInstallationEvent(context.Background(), nil, nil, []byte("{not json"))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestHandleInstallationEvent_UnknownActionIsNoop(t *testing.T) {
	body := []byte(`{"action":"created_repository","installation":{"id":1}}`)
	if err := handleInstallationEvent(context.Background(), nil, nil, body); err != nil {
		t.Errorf("expected no-op for unknown action, got %v", err)
	}
}

func TestNotifyInstallationChange_NilChannelSafe(t *testing.T) {
	// Must not panic on nil — middleware sometimes passes a nil chan when
	// installation change tracking is disabled.
	notifyInstallationChange(nil)
}

func TestNotifyInstallationChange_NonBlocking(t *testing.T) {
	ch := make(chan struct{}, 1)
	// First send fills the buffer; second must not block (uses select-default).
	notifyInstallationChange(ch)
	notifyInstallationChange(ch)
	if len(ch) != 1 {
		t.Errorf("ch buffered = %d, want 1 (second send dropped via default)", len(ch))
	}
}

func TestWebhookHandlerUnknownEvent(t *testing.T) {
	handler := webhookHandler(nil, nil, "secret", nil)

	body := []byte(`{}`)
	sig := computeHMAC(body, "secret")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "ping")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
