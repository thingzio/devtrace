package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
)

// eventsPerPage is the page size for the watchlist activity table.
const eventsPerPage = 10

// maxFilterLen caps user-supplied filter strings to prevent abusive query sizes.
const maxFilterLen = 64

// eventDTO is the serialized shape of NotificationEvent for the JSON API.
// Pre-formats RepoSummary/RepoTooltip/DetailSummary so the client renders
// without re-implementing the helper logic.
type eventDTO struct {
	EventType     string `json:"event_type"`
	Username      string `json:"username"`
	Target        string `json:"target"`
	RepoSummary   string `json:"repo_summary"`
	RepoTooltip   string `json:"repo_tooltip"`
	DetailSummary string `json:"detail_summary"`
	CreatedAt     string `json:"created_at"`
}

type eventsResponse struct {
	Events     []eventDTO `json:"events"`
	Page       int        `json:"page"`
	Total      int        `json:"total"`
	TotalPages int        `json:"total_pages"`
	HasPrev    bool       `json:"has_prev"`
	HasNext    bool       `json:"has_next"`
}

// parseEventFilter extracts contributor/repo/since from the query string and
// caps free-text fields. since accepts either an RFC3339 timestamp or a
// YYYY-MM-DD date (interpreted as midnight UTC).
func parseEventFilter(r *http.Request) (postgres.NotificationEventFilter, int) {
	q := r.URL.Query()

	contributor := strings.TrimSpace(q.Get("contributor"))
	if len(contributor) > maxFilterLen {
		contributor = contributor[:maxFilterLen]
	}
	org := strings.TrimSpace(q.Get("org"))
	if len(org) > maxFilterLen {
		org = org[:maxFilterLen]
	}
	repo := strings.TrimSpace(q.Get("repo"))
	if len(repo) > maxFilterLen {
		repo = repo[:maxFilterLen]
	}

	var since time.Time
	if s := strings.TrimSpace(q.Get("since")); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			since = t
		} else if t, err := time.Parse("2006-01-02", s); err == nil {
			since = t
		}
	}

	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	return postgres.NotificationEventFilter{
		Contributor: contributor,
		Org:         org,
		Repo:        repo,
		Since:       since,
	}, page
}

// fetchEvents queries the store and shapes the response for either the
// initial server-rendered dashboard or the JSON endpoint.
func fetchEvents(r *http.Request, store *postgres.Store, tenantID string, filter postgres.NotificationEventFilter, page int) (eventsResponse, []postgres.NotificationEvent, error) {
	offset := (page - 1) * eventsPerPage
	events, total, err := store.GetNotificationEvents(r.Context(), tenantID, filter, eventsPerPage, offset)
	if err != nil {
		return eventsResponse{}, nil, err
	}

	totalPages := (total + eventsPerPage - 1) / eventsPerPage
	if totalPages < 1 {
		totalPages = 1
	}

	dtos := make([]eventDTO, 0, len(events))
	for _, ev := range events {
		dtos = append(dtos, eventDTO{
			EventType:     ev.EventType,
			Username:      ev.Username,
			Target:        ev.Target,
			RepoSummary:   ev.RepoSummary(),
			RepoTooltip:   ev.RepoTooltip(),
			DetailSummary: ev.DetailSummary(),
			CreatedAt:     ev.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	return eventsResponse{
		Events:     dtos,
		Page:       page,
		Total:      total,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}, events, nil
}

// dashboardEventsHandler returns watchlist activity as JSON for client-side
// table updates. Per-column filters: contributor, repo, since (date or
// RFC3339). Results are scoped to the authenticated tenant.
func dashboardEventsHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		filter, page := parseEventFilter(r)
		resp, _, err := fetchEvents(r, store, tn.ID, filter, page)
		if err != nil {
			slog.Error("dashboard events: fetch", "tenant", tn.ID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch events"})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
