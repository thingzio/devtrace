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
	"log/slog"
	"net/http"
	"regexp"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/watchlist"
)

// validTarget matches GitHub org names or org/repo patterns.
var validTarget = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?(/[a-zA-Z0-9._-]+)?$`)

func addWatchlistHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, authGitHubPath, http.StatusFound)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		target := r.FormValue("target")
		if target == "" || !validTarget.MatchString(target) || len(target) > 100 {
			http.Redirect(w, r, "/settings?msg=invalid_target", http.StatusSeeOther)
			return
		}

		p, ok := plan.Get(tn.Plan)
		if !ok {
			p = plan.Free()
		}

		manualCount, err := store.WatchlistManualCount(r.Context(), tn.ID)
		if err != nil {
			slog.Error("watchlist count", "tenant", tn.ID, "error", err)
			http.Redirect(w, r, "/settings?msg=watchlist_limit_error", http.StatusSeeOther)
			return
		}

		if manualCount >= p.MaxWatchlists {
			http.Redirect(w, r, "/settings?msg=watchlist_limit", http.StatusSeeOther)
			return
		}

		if err := store.CreateWatchlist(r.Context(), tn.ID, target, "manual"); err != nil {
			slog.Error("create watchlist", "tenant", tn.ID, "target", target, "error", err)
			http.Redirect(w, r, "/settings?msg=watchlist_add_error", http.StatusSeeOther)
			return
		}

		slog.Info("watchlist created", "tenant", tn.Username, "target", target)
		http.Redirect(w, r, "/settings?msg=watchlist_added", http.StatusSeeOther)
	}
}

func deleteWatchlistHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, authGitHubPath, http.StatusFound)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Redirect(w, r, "/settings?msg=invalid_request", http.StatusSeeOther)
			return
		}

		if err := store.DeleteWatchlist(r.Context(), id, tn.ID); err != nil {
			slog.Error("delete watchlist", "tenant", tn.ID, "id", id, "error", err)
			http.Redirect(w, r, "/settings?msg=watchlist_delete_denied", http.StatusSeeOther)
			return
		}

		slog.Info("watchlist deleted", "tenant", tn.Username, "id", id)
		http.Redirect(w, r, "/settings?msg=watchlist_removed", http.StatusSeeOther)
	}
}

func digestUnsubscribeHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenant")
		token := r.URL.Query().Get("token")
		secret := watchlist.HMACSecret()

		if tenantID == "" || token == "" || secret == "" {
			http.Error(w, "invalid unsubscribe link", http.StatusBadRequest)
			return
		}

		if !watchlist.ValidateUnsubscribeToken(secret, tenantID, token) {
			http.Error(w, "invalid unsubscribe link", http.StatusForbidden)
			return
		}

		if err := store.DisableWatchlistEmails(r.Context(), tenantID); err != nil {
			slog.Error("unsubscribe: disable emails", "tenant", tenantID, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		slog.Info("digest unsubscribed", "tenant", tenantID)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><meta charset="utf-8">` +
			`<title>Unsubscribed</title></head>` +
			`<body style="margin:0;padding:0;background:#0c1017;color:#f0f0f0;` +
			`font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">` +
			`<div style="max-width:480px;margin:80px auto;text-align:center;">` +
			`<h1 style="font-size:20px;margin:0 0 12px;">Unsubscribed</h1>` +
			`<p style="color:#999;font-size:14px;">You will no longer receive weekly digest emails.</p>` +
			`<p style="margin-top:24px;"><a href="/settings" ` +
			`style="color:#58a6ff;text-decoration:underline;">Manage watchlists</a></p>` +
			`</div></body></html>`))
	}
}

func toggleWatchlistEmailHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, authGitHubPath, http.StatusFound)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Redirect(w, r, "/settings?msg=invalid_request", http.StatusSeeOther)
			return
		}

		if err := store.ToggleWatchlistEmail(r.Context(), id, tn.ID); err != nil {
			slog.Error("toggle watchlist email", "tenant", tn.ID, "id", id, "error", err)
			http.Redirect(w, r, "/settings?msg=watchlist_update_error", http.StatusSeeOther)
			return
		}

		http.Redirect(w, r, "/settings", http.StatusSeeOther)
	}
}
