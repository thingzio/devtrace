package server

import (
	"log/slog"
	"net/http"
	"regexp"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
)

// validTarget matches GitHub org names or org/repo patterns.
var validTarget = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?(/[a-zA-Z0-9._-]+)?$`)

func addWatchlistHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		target := r.FormValue("target")
		if target == "" || !validTarget.MatchString(target) || len(target) > 100 {
			http.Redirect(w, r, "/settings?msg=Invalid+org+or+repo+name", http.StatusSeeOther)
			return
		}

		p, ok := plan.Get(tn.Plan)
		if !ok {
			p = plan.Free()
		}

		manualCount, err := store.WatchlistManualCount(r.Context(), tn.ID)
		if err != nil {
			slog.Error("watchlist count", "tenant", tn.ID, "error", err)
			http.Redirect(w, r, "/settings?msg=Error+checking+watchlist+limit", http.StatusSeeOther)
			return
		}

		if manualCount >= p.MaxWatchlists {
			http.Redirect(w, r, "/settings?msg=Watchlist+limit+reached", http.StatusSeeOther)
			return
		}

		if err := store.CreateWatchlist(r.Context(), tn.ID, target, "manual"); err != nil {
			slog.Error("create watchlist", "tenant", tn.ID, "target", target, "error", err)
			http.Redirect(w, r, "/settings?msg=Error+adding+watchlist", http.StatusSeeOther)
			return
		}

		slog.Info("watchlist created", "tenant", tn.Username, "target", target)
		http.Redirect(w, r, "/settings?msg=Watchlist+added", http.StatusSeeOther)
	}
}

func deleteWatchlistHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Redirect(w, r, "/settings?msg=Invalid+request", http.StatusSeeOther)
			return
		}

		if err := store.DeleteWatchlist(r.Context(), id, tn.ID); err != nil {
			slog.Error("delete watchlist", "tenant", tn.ID, "id", id, "error", err)
			http.Redirect(w, r, "/settings?msg=Cannot+delete+this+watchlist", http.StatusSeeOther)
			return
		}

		slog.Info("watchlist deleted", "tenant", tn.Username, "id", id)
		http.Redirect(w, r, "/settings?msg=Watchlist+removed", http.StatusSeeOther)
	}
}

func toggleWatchlistEmailHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Redirect(w, r, "/settings?msg=Invalid+request", http.StatusSeeOther)
			return
		}

		if err := store.ToggleWatchlistEmail(r.Context(), id, tn.ID); err != nil {
			slog.Error("toggle watchlist email", "tenant", tn.ID, "id", id, "error", err)
			http.Redirect(w, r, "/settings?msg=Error+updating+watchlist", http.StatusSeeOther)
			return
		}

		http.Redirect(w, r, "/settings", http.StatusSeeOther)
	}
}
