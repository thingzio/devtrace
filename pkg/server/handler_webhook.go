package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func webhookHandler(db *sql.DB, secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const maxWebhookBytes = 1 << 20 // 1MB
		body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBytes))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if !verifySignature(body, r.Header.Get("X-Hub-Signature-256"), secret) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		event := r.Header.Get("X-GitHub-Event")
		switch event {
		case "installation":
			if err := handleInstallationEvent(r.Context(), db, body); err != nil {
				slog.Error("installation event", "error", err)
				http.Error(w, "processing failed", http.StatusInternalServerError)
				return
			}
		default:
			slog.Debug("ignoring webhook event", "event", event)
		}

		w.WriteHeader(http.StatusOK)
	}
}

func verifySignature(payload []byte, signature, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hmac.Equal(sig, mac.Sum(nil))
}

func handleInstallationEvent(ctx context.Context, db *sql.DB, body []byte) error {
	var event struct {
		Action       string `json:"action"`
		Installation struct {
			ID      int64 `json:"id"`
			Account struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"account"`
		} `json:"installation"`
		Sender struct {
			ID int64 `json:"id"`
		} `json:"sender"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return fmt.Errorf("parsing installation event: %w", err)
	}

	switch event.Action {
	case "created":
		tn, err := tenant.GetTenantByGitHubID(ctx, db, event.Sender.ID)
		if err != nil {
			return fmt.Errorf("finding tenant for sender %d: %w", event.Sender.ID, err)
		}
		return tenant.SaveInstallation(ctx, db, tn.ID,
			event.Installation.ID,
			event.Installation.Account.Type,
			event.Installation.Account.Login)

	case "deleted", "suspend":
		return tenant.SuspendInstallation(ctx, db, event.Installation.ID)
	}

	return nil
}
