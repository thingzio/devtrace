package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// WatchlistEntry represents a single watchlist target with its metadata.
type WatchlistEntry struct {
	ID       string
	TenantID string
	Target   string
	Source   string
	Plan     string // tenant plan, populated by bulk queries
}

// Watchlist represents a full watchlist row for display in settings.
type Watchlist struct {
	ID          string
	TenantID    string
	Target      string
	Source      string
	NotifyEmail bool
	CreatedAt   time.Time
}

// NotificationEvent represents a detected watchlist event for display.
type NotificationEvent struct {
	ID        int64
	EventType string
	Username  string
	Target    string // joined from watchlist
	Details   map[string]any
	CreatedAt time.Time
	SentAt    *time.Time
}

// DetailSummary returns a human-readable verb phrase. Never returns "" for
// known event types; falls back to "first activity" when no counts are present.
func (e NotificationEvent) DetailSummary() string {
	switch e.EventType {
	case "score_change":
		old, _ := e.Details["old_grade"].(string)
		newG, _ := e.Details["new_grade"].(string)
		if old != "" && newG != "" {
			return "grade " + old + " → " + newG
		}
		return "grade changed"
	case "new_contributor":
		var phrases []string
		if n := intDetail(e.Details, "prs_opened"); n > 0 {
			phrases = append(phrases, fmt.Sprintf("opened %d %s", n, plural(n, "PR", "PRs")))
		}
		if n := intDetail(e.Details, "prs_merged"); n > 0 {
			phrases = append(phrases, fmt.Sprintf("merged %d %s", n, plural(n, "PR", "PRs")))
		}
		if n := intDetail(e.Details, "reviews_given"); n > 0 {
			phrases = append(phrases, fmt.Sprintf("%d %s", n, plural(n, "review", "reviews")))
		}
		if n := intDetail(e.Details, "issues_opened"); n > 0 {
			phrases = append(phrases, fmt.Sprintf("opened %d %s", n, plural(n, "issue", "issues")))
		}
		if n := intDetail(e.Details, "issue_comments"); n > 0 {
			phrases = append(phrases, fmt.Sprintf("%d %s", n, plural(n, "comment", "comments")))
		}
		if len(phrases) == 0 {
			return "first activity"
		}
		// Cap at 2 phrases to keep the cell compact.
		if len(phrases) > 2 {
			phrases = phrases[:2]
		}
		return strings.Join(phrases, ", ")
	}
	return e.EventType
}

func plural(n int, singular, plur string) string {
	if n == 1 {
		return singular
	}
	return plur
}

// RepoSummary returns the Repo column display string. Empty when no repos
// are recorded. For a single repo, returns the bare repo name (org prefix
// stripped). For multiple repos, returns "first +N" where N is the count of
// remaining repos.
func (e NotificationEvent) RepoSummary() string {
	repos := repoStrings(e.Details)
	if len(repos) == 0 {
		return ""
	}
	first := stripOrgPrefix(repos[0])
	if len(repos) == 1 {
		return first
	}
	return fmt.Sprintf("%s +%d", first, len(repos)-1)
}

// RepoTooltip returns the full comma-joined list of repos for the HTML
// title attribute. Returns "" when no repos are recorded.
func (e NotificationEvent) RepoTooltip() string {
	repos := repoStrings(e.Details)
	if len(repos) == 0 {
		return ""
	}
	return strings.Join(repos, ", ")
}

func repoStrings(d map[string]any) []string {
	raw, ok := d["repos"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func stripOrgPrefix(repo string) string {
	if idx := strings.Index(repo, "/"); idx > 0 {
		return repo[idx+1:]
	}
	return repo
}

func intDetail(d map[string]any, key string) int {
	v, ok := d[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// DigestTarget holds tenant info for sending a digest email.
type DigestTarget struct {
	TenantID string
	Email    string
	Username string
	Plan     string
}

// GetAllWatchlistTargets returns all active watchlist targets grouped by target.
// Each target maps to a slice of watchlist entries so the ingest pipeline
// can match contributor repos against watchlists.
func (s *Store) GetAllWatchlistTargets(ctx context.Context) (map[string][]WatchlistEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.id, w.tenant_id, w.target, w.source, t.plan
		 FROM devtrace_watchlist w
		 JOIN devtrace_tenant t ON t.id = w.tenant_id
		 WHERE t.status = 'active'`)
	if err != nil {
		return nil, fmt.Errorf("get watchlist targets: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]WatchlistEntry)
	for rows.Next() {
		var e WatchlistEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Target, &e.Source, &e.Plan); err != nil {
			return nil, fmt.Errorf("scan watchlist entry: %w", err)
		}
		result[e.Target] = append(result[e.Target], e)
	}
	return result, rows.Err()
}

// InsertNotificationEvent records a new watchlist notification event.
func (s *Store) InsertNotificationEvent(ctx context.Context, watchlistID, eventType, username string, details map[string]any) error {
	var detailsJSON []byte
	if details != nil {
		var err error
		detailsJSON, err = json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshal notification details: %w", err)
		}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devtrace_notification_event (watchlist_id, event_type, username, details)
		 VALUES ($1, $2, $3, $4::jsonb)`,
		watchlistID, eventType, username, detailsJSON)
	if err != nil {
		return fmt.Errorf("insert notification event: %w", err)
	}
	return nil
}

// GetNotificationEvents returns paginated notification events for a tenant,
// optionally filtered by a query string matching username, target, or any
// repo in details->'repos'. The query is matched case-insensitively as a
// substring. An empty query returns all events. Events are ordered by
// created_at DESC. Returns the events and the total count of the filtered set.
func (s *Store) GetNotificationEvents(ctx context.Context, tenantID, query string, limit, offset int) ([]NotificationEvent, int, error) {
	const filterClause = `
		WHERE w.tenant_id = $1
		  AND ($2 = ''
		       OR LOWER(ne.username) LIKE '%' || $2 || '%'
		       OR LOWER(w.target)    LIKE '%' || $2 || '%'
		       OR EXISTS (
		            SELECT 1 FROM jsonb_array_elements_text(ne.details->'repos') r
		            WHERE LOWER(r) LIKE '%' || $2 || '%'
		       ))`

	q := strings.ToLower(query)

	var total int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_notification_event ne
		 JOIN devtrace_watchlist w ON w.id = ne.watchlist_id`+filterClause,
		tenantID, q).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count notification events: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT ne.id, ne.event_type, ne.username, w.target, ne.details, ne.created_at, ne.sent_at
		 FROM devtrace_notification_event ne
		 JOIN devtrace_watchlist w ON w.id = ne.watchlist_id`+filterClause+`
		 ORDER BY ne.created_at DESC
		 LIMIT $3 OFFSET $4`, tenantID, q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query notification events: %w", err)
	}
	defer rows.Close()

	var events []NotificationEvent
	for rows.Next() {
		var ev NotificationEvent
		var rawDetails []byte
		var sentAt sql.NullTime
		if err := rows.Scan(&ev.ID, &ev.EventType, &ev.Username, &ev.Target, &rawDetails, &ev.CreatedAt, &sentAt); err != nil {
			return nil, 0, fmt.Errorf("scan notification event: %w", err)
		}
		if sentAt.Valid {
			ev.SentAt = &sentAt.Time
		}
		if len(rawDetails) > 0 {
			_ = json.Unmarshal(rawDetails, &ev.Details)
		}
		events = append(events, ev)
	}
	return events, total, rows.Err()
}

// GetUnsentEventsForDigest returns unsent notification events for a tenant, capped at limit.
func (s *Store) GetUnsentEventsForDigest(ctx context.Context, tenantID string, limit int) ([]NotificationEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ne.id, ne.event_type, ne.username, w.target, ne.details, ne.created_at
		 FROM devtrace_notification_event ne
		 JOIN devtrace_watchlist w ON w.id = ne.watchlist_id
		 WHERE w.tenant_id = $1 AND ne.sent_at IS NULL
		 ORDER BY ne.created_at DESC
		 LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("query unsent events: %w", err)
	}
	defer rows.Close()

	var events []NotificationEvent
	for rows.Next() {
		var ev NotificationEvent
		var rawDetails []byte
		if err := rows.Scan(&ev.ID, &ev.EventType, &ev.Username, &ev.Target, &rawDetails, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan unsent event: %w", err)
		}
		if len(rawDetails) > 0 {
			_ = json.Unmarshal(rawDetails, &ev.Details)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// MarkAllEventsSent marks all unsent notification events for a tenant as sent.
// Called after a digest email is sent — the email contains the top N events but
// all pending events are cleared so they don't queue up for the next digest.
func (s *Store) MarkAllEventsSent(ctx context.Context, tenantID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE devtrace_notification_event SET sent_at = NOW()
		 WHERE sent_at IS NULL
		   AND watchlist_id IN (SELECT id FROM devtrace_watchlist WHERE tenant_id = $1)`,
		tenantID)
	if err != nil {
		return fmt.Errorf("mark all events sent: %w", err)
	}
	return nil
}

// ListWatchlists returns all watchlists for a tenant.
func (s *Store) ListWatchlists(ctx context.Context, tenantID string) ([]Watchlist, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tenant_id, target, source, notify_email, created_at
		 FROM devtrace_watchlist
		 WHERE tenant_id = $1
		 ORDER BY source ASC, created_at ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list watchlists: %w", err)
	}
	defer rows.Close()

	var result []Watchlist
	for rows.Next() {
		var w Watchlist
		if err := rows.Scan(&w.ID, &w.TenantID, &w.Target, &w.Source, &w.NotifyEmail, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan watchlist: %w", err)
		}
		result = append(result, w)
	}
	return result, rows.Err()
}

// CreateWatchlist adds a manual watchlist entry. Uses ON CONFLICT DO NOTHING for idempotency.
func (s *Store) CreateWatchlist(ctx context.Context, tenantID, target, source string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devtrace_watchlist (tenant_id, target, source)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, target) DO NOTHING`,
		tenantID, target, source)
	if err != nil {
		return fmt.Errorf("create watchlist: %w", err)
	}
	return nil
}

// DeleteWatchlist removes a manual watchlist entry. The tenant_id check ensures
// a tenant can only delete their own watchlists.
func (s *Store) DeleteWatchlist(ctx context.Context, watchlistID, tenantID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM devtrace_watchlist WHERE id = $1 AND tenant_id = $2 AND source = 'manual'`,
		watchlistID, tenantID)
	if err != nil {
		return fmt.Errorf("delete watchlist: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("delete watchlist: not found or implicit")
	}
	return nil
}

// ToggleWatchlistEmail flips the notify_email flag for a watchlist entry.
func (s *Store) ToggleWatchlistEmail(ctx context.Context, watchlistID, tenantID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE devtrace_watchlist SET notify_email = NOT notify_email
		 WHERE id = $1 AND tenant_id = $2`,
		watchlistID, tenantID)
	if err != nil {
		return fmt.Errorf("toggle watchlist email: %w", err)
	}
	return nil
}

// DisableWatchlistEmails sets notify_email = false for all watchlists belonging to a tenant.
// Used by the one-click unsubscribe handler.
func (s *Store) DisableWatchlistEmails(ctx context.Context, tenantID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE devtrace_watchlist SET notify_email = FALSE WHERE tenant_id = $1`,
		tenantID)
	if err != nil {
		return fmt.Errorf("disable watchlist emails: %w", err)
	}
	return nil
}

// EnsureImplicitWatchlist creates an implicit watchlist entry for a GitHub App installation.
// Idempotent: does nothing if the entry already exists.
func (s *Store) EnsureImplicitWatchlist(ctx context.Context, tenantID, targetLogin string) error {
	return s.CreateWatchlist(ctx, tenantID, targetLogin, "implicit")
}

// GetCurrentGrade returns the current letter grade for a contributor.
// Returns "" if no reputation record exists.
func (s *Store) GetCurrentGrade(ctx context.Context, username, provider string) (string, error) {
	var grade string
	err := s.db.QueryRowContext(ctx,
		`SELECT grade FROM devtrace_reputation WHERE username = $1 AND provider = $2`,
		username, provider).Scan(&grade)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get current grade: %w", err)
	}
	return grade, nil
}

// GetWatchlistsForContributor returns watchlist entries matching a contributor's
// recent activity repos. Joins activity repos against watchlist targets.
func (s *Store) GetWatchlistsForContributor(ctx context.Context, username, provider string) ([]WatchlistEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT w.id, w.tenant_id, w.target, w.source, t.plan
		 FROM devtrace_watchlist w
		 JOIN devtrace_tenant t ON t.id = w.tenant_id
		 WHERE t.status = 'active'
		   AND EXISTS (
		     SELECT 1 FROM devtrace_contributor_activity ca,
		       jsonb_array_elements_text(ca.repos) r
		     WHERE ca.username = $1 AND ca.provider = $2
		       AND ca.hour > NOW() - INTERVAL '90 days'
		       AND (r = w.target OR split_part(r, '/', 1) = w.target)
		   )`, username, provider)
	if err != nil {
		return nil, fmt.Errorf("get watchlists for contributor: %w", err)
	}
	defer rows.Close()

	var result []WatchlistEntry
	for rows.Next() {
		var e WatchlistEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Target, &e.Source, &e.Plan); err != nil {
			return nil, fmt.Errorf("scan watchlist for contributor: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// GetTenantsWithUnsentEvents returns tenants that have unsent notification events
// and have email-enabled watchlists.
func (s *Store) GetTenantsWithUnsentEvents(ctx context.Context) ([]DigestTarget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT t.id, t.email, t.username, t.plan
		 FROM devtrace_tenant t
		 JOIN devtrace_watchlist w ON w.tenant_id = t.id
		 JOIN devtrace_notification_event ne ON ne.watchlist_id = w.id
		 WHERE t.status = 'active'
		   AND t.email != ''
		   AND w.notify_email = TRUE
		   AND ne.sent_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("get tenants with unsent events: %w", err)
	}
	defer rows.Close()

	var result []DigestTarget
	for rows.Next() {
		var d DigestTarget
		if err := rows.Scan(&d.TenantID, &d.Email, &d.Username, &d.Plan); err != nil {
			return nil, fmt.Errorf("scan digest target: %w", err)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// WatchlistManualCount returns the number of manual watchlist entries for a tenant.
func (s *Store) WatchlistManualCount(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_watchlist WHERE tenant_id = $1 AND source = 'manual'`,
		tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("watchlist manual count: %w", err)
	}
	return count, nil
}

// LastDigestSentAt returns the most recent sent_at time for a tenant's digest events.
// Returns nil if no digest has been sent yet.
func (s *Store) LastDigestSentAt(ctx context.Context, tenantID string) (*time.Time, error) {
	var t *time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(ne.sent_at)
		 FROM devtrace_notification_event ne
		 JOIN devtrace_watchlist w ON w.id = ne.watchlist_id
		 WHERE w.tenant_id = $1 AND ne.sent_at IS NOT NULL`,
		tenantID).Scan(&t)
	if err != nil {
		return nil, fmt.Errorf("last digest sent at: %w", err)
	}
	return t, nil
}

// UnsentEventCount returns the total number of unsent events for a tenant.
func (s *Store) UnsentEventCount(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		 FROM devtrace_notification_event ne
		 JOIN devtrace_watchlist w ON w.id = ne.watchlist_id
		 WHERE w.tenant_id = $1 AND ne.sent_at IS NULL`,
		tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("unsent event count: %w", err)
	}
	return count, nil
}
