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
