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

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func webhookHandler(db *sql.DB, store *postgres.Store, secret string, installNotify chan<- struct{}) http.HandlerFunc {
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
			if err := handleInstallationEvent(r.Context(), db, store, body); err != nil {
				slog.Error("installation event", "error", err)
				http.Error(w, "processing failed", http.StatusInternalServerError)
				return
			}
			notifyInstallationChange(installNotify)
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

func handleInstallationEvent(ctx context.Context, db *sql.DB, store *postgres.Store, body []byte) error {
	var event struct {
		Action       string `json:"action"`
		Installation struct {
			ID      int64 `json:"id"`
			AppID   int64 `json:"app_id"`
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

	slog.Info("installation event",
		"action", event.Action,
		"account", event.Installation.Account.Login,
	)

	switch event.Action {
	case "created":
		tn, err := tenant.GetTenantByGitHubID(ctx, db, event.Sender.ID)
		if err != nil {
			return fmt.Errorf("finding tenant for sender %d: %w", event.Sender.ID, err)
		}
		if err := tenant.SaveInstallation(ctx, db, tn.ID,
			event.Installation.ID,
			event.Installation.AppID,
			event.Installation.Account.Type,
			event.Installation.Account.Login); err != nil {
			return fmt.Errorf("saving installation for tenant %s: %w", tn.ID, err)
		}
		// Counterpart to the "install nudge shown" impression: together these
		// give install conversion a denominator. account_type distinguishes
		// personal from organization installs, which is what the settings
		// copy steers people toward.
		slog.Info("installation created",
			"tenant", tn.ID,
			"login", event.Installation.Account.Login,
			"account_type", event.Installation.Account.Type,
			"installation_id", event.Installation.ID,
		)

		// Auto-create implicit watchlist for the installed org.
		if store != nil {
			if wlErr := store.EnsureImplicitWatchlist(ctx, tn.ID, event.Installation.Account.Login); wlErr != nil {
				slog.Error("auto-create watchlist", "tenant", tn.ID, "login", event.Installation.Account.Login, "error", wlErr)
			}
		}
		return nil

	case "deleted", "suspend":
		return tenant.SuspendInstallation(ctx, db, event.Installation.ID)
	}

	return nil
}

func notifyInstallationChange(ch chan<- struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}
