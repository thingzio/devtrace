package ingest

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	devnet "github.com/thingzio/devtrace/pkg/net"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/watchlist"
)

const (
	syncStateKey     = "gharchive_cursor"
	compactStateKey  = "activity_compacted"
	digestStateKey   = "digest_email_sweep"
	compactInterval  = 24 * time.Hour
	digestInterval   = 24 * time.Hour      // sweep daily; per-tenant day assignment ensures weekly delivery
	compactOlderThan = 30 * 24 * time.Hour // aggregate rows older than 30 days

	pruneActivityRetention = 120 * 24 * time.Hour // delete activity older than 120 days
	pruneHistoryRetention  = 400 * 24 * time.Hour // delete score history older than 400 days

	digestMaxEventsPerEmail = 10
)

// ingestStore defines the store operations needed by the ingest runner.
type ingestStore interface {
	GetSyncState(ctx context.Context, key string) (time.Time, error)
	SaveSyncState(ctx context.Context, key string, val time.Time) error
	GetTenantRepos(ctx context.Context) (map[string]bool, error)
	BatchUpsertActivity(ctx context.Context, summaries []postgres.HourlySummary) (int, error)
	EnqueueForScoring(ctx context.Context, username, provider string, priority int) error
	ContributorExists(ctx context.Context, username, provider string) (bool, error)
	PurgeNonTenantQueue(ctx context.Context) (int64, error)
	CompactActivity(ctx context.Context, olderThan time.Duration) (int64, error)
	PruneActivity(ctx context.Context, retention time.Duration) (int64, error)
	PruneScoreHistory(ctx context.Context, retention time.Duration) (int64, error)
	GetAllWatchlistTargets(ctx context.Context) (map[string][]postgres.WatchlistEntry, error)
	InsertNotificationEvent(ctx context.Context, watchlistID, eventType, username string, details map[string]any) error
	GetTenantsWithUnsentEvents(ctx context.Context) ([]postgres.DigestTarget, error)
	GetUnsentEventsForDigest(ctx context.Context, tenantID string, limit int) ([]postgres.NotificationEvent, error)
	MarkAllEventsSent(ctx context.Context, tenantID string) error
}

// Run processes one or more hourly GH Archive dumps.
func Run(ctx context.Context, store *postgres.Store) error {
	baseURL := config.GetEnv("GHARCHIVE_BASE_URL", "")
	reader := NewArchiveReader(baseURL)

	cursor, err := store.GetSyncState(ctx, syncStateKey)
	if err != nil {
		slog.Warn("get sync state failed, starting from scratch", "error", err)
	}
	lookback := config.GetEnvAsInt("GHARCHIVE_LOOKBACK_HOURS", 1)
	catchupMax := config.GetEnvAsInt("GHARCHIVE_CATCHUP_MAX_HOURS", 24)
	hours := computeHours(cursor, lookback, catchupMax)

	if len(hours) == 0 {
		slog.Info("no hours to process")
		return nil
	}

	tenantRepos, err := store.GetTenantRepos(ctx)
	if err != nil {
		slog.Warn("get tenant repos failed, proceeding without tenant filter", "error", err)
	}

	watchlistTargets, err := store.GetAllWatchlistTargets(ctx)
	if err != nil {
		slog.Warn("get watchlist targets failed", "error", err)
	}
	slog.Info("ingest starting", "hours", len(hours), "tenant_orgs", len(tenantRepos), "watchlist_targets", len(watchlistTargets))

	for _, hour := range hours {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := processHour(ctx, store, reader, hour, tenantRepos, watchlistTargets); err != nil {
			if errors.Is(err, ErrArchiveNotFound) {
				slog.Warn("archive not yet available", "hour", hour)
				break // later hours won't be available either
			}
			slog.Error("process hour failed", "hour", hour, "error", err)
			continue
		}
		if err := store.SaveSyncState(ctx, syncStateKey, hour); err != nil {
			slog.Error("save cursor", "error", err)
		}
	}

	// Compaction: aggregate old hourly rows into weekly buckets.
	maybeCompact(ctx, store)

	// Digest: send weekly notification emails.
	maybeSendDigests(ctx, store)

	return nil
}

// maybeCompact runs activity compaction if it hasn't run in the last 24 hours.
// Errors are logged but do not fail the ingest run.
func maybeCompact(ctx context.Context, store ingestStore) {
	lastCompact, err := store.GetSyncState(ctx, compactStateKey)
	if err != nil {
		slog.Warn("get compaction state failed", "error", err)
	}
	if !lastCompact.IsZero() && time.Since(lastCompact) < compactInterval {
		return
	}

	slog.Info("running activity compaction", "older_than", compactOlderThan)
	deleted, err := store.CompactActivity(ctx, compactOlderThan)
	if err != nil {
		slog.Error("compaction failed", "error", err)
		return
	}

	slog.Info("compaction complete", "rows_deleted", deleted)

	// Prune old activity rows beyond retention window.
	pruned, err := store.PruneActivity(ctx, pruneActivityRetention)
	if err != nil {
		slog.Error("prune activity failed", "error", err)
	} else if pruned > 0 {
		slog.Info("pruned old activity", "rows_deleted", pruned)
	}

	// Prune old score history beyond retention window.
	histPruned, err := store.PruneScoreHistory(ctx, pruneHistoryRetention)
	if err != nil {
		slog.Error("prune score history failed", "error", err)
	} else if histPruned > 0 {
		slog.Info("pruned old score history", "rows_deleted", histPruned)
	}

	if err := store.SaveSyncState(ctx, compactStateKey, time.Now().UTC()); err != nil {
		slog.Error("save compaction state", "error", err)
	}
}

// computeHours determines which hourly archive files to process.
//   - No cursor (fresh install): returns up to lookback hours ending at now-1h.
//   - With cursor (catching up): returns up to catchupMax hours from cursor+1h.
func computeHours(cursor time.Time, lookback, catchupMax int) []time.Time {
	now := time.Now().UTC().Truncate(time.Hour)
	lastAvailable := now.Add(-config.ArchivePublishDelay)

	var start time.Time
	var cap int
	if cursor.IsZero() {
		start = lastAvailable.Add(-time.Duration(lookback-1) * time.Hour)
		cap = lookback
	} else {
		start = cursor.Add(time.Hour)
		cap = catchupMax
	}

	if !start.Before(now) {
		return nil
	}

	var hours []time.Time
	for t := start; !t.After(lastAvailable) && len(hours) < cap; t = t.Add(time.Hour) {
		hours = append(hours, t)
	}
	return hours
}

func processHour(ctx context.Context, store ingestStore, reader *ArchiveReader,
	hour time.Time, tenantRepos map[string]bool, watchlistTargets map[string][]postgres.WatchlistEntry) error {
	start := time.Now()
	slog.Info("processing archive", "hour", hour.Format("2006-01-02-15"))

	agg := NewAggregator(hour)
	var eventCount int

	err := reader.Stream(ctx, hour, func(ev Event) {
		agg.Add(ev)
		eventCount++
	})
	if err != nil {
		return fmt.Errorf("stream archive %s: %w", hour.Format("2006-01-02-15"), err)
	}

	results := agg.Results()

	pgSummaries := make([]postgres.HourlySummary, 0, len(results))
	for _, s := range results {
		repos := make([]string, 0, len(s.Repos))
		for r := range s.Repos {
			repos = append(repos, r)
		}
		pgSummaries = append(pgSummaries, postgres.HourlySummary{
			Username:      s.Username,
			Provider:      "github",
			Hour:          agg.Hour(),
			PRsOpened:     s.PRsOpened,
			PRsMerged:     s.PRsMerged,
			PRsClosed:     s.PRsClosed,
			ReviewsGiven:  s.ReviewsGiven,
			IssueComments: s.IssueComments,
			IssuesOpened:  s.IssuesOpened,
			IssuesClosed:  s.IssuesClosed,
			DistinctRepos: len(repos),
			Repos:         repos,
		})
	}

	stored, err := store.BatchUpsertActivity(ctx, pgSummaries)
	if err != nil {
		return fmt.Errorf("batch upsert activity %s: %w", hour.Format("2006-01-02-15"), err)
	}

	qr := queueContributors(ctx, store, results, tenantRepos, watchlistTargets)

	slog.Info("archive hour complete",
		"hour", hour.Format("2006-01-02-15"),
		"mode", "hourly",
		"events", eventCount,
		"contributors", len(results),
		"stored", stored,
		"queued", qr.queued,
		"skipped_non_tenant", qr.skippedNonTenant,
		"duration_sec", time.Since(start).Seconds(),
	)

	return nil
}

type queueResult struct {
	queued           int
	skippedNonTenant int
}

func queueContributors(ctx context.Context, store ingestStore,
	summaries []Summary, tenantRepos map[string]bool, watchlistTargets map[string][]postgres.WatchlistEntry) queueResult {
	var qr queueResult
	for _, s := range summaries {
		exists, _ := store.ContributorExists(ctx, s.Username, "github")

		touchesTenant := false
		for repo := range s.Repos {
			owner := repo
			if idx := strings.Index(repo, "/"); idx > 0 {
				owner = repo[:idx]
			}
			if tenantRepos[owner] {
				touchesTenant = true
				break
			}
		}

		// Watchlist: detect new contributors for watched orgs/repos.
		if !exists && len(watchlistTargets) > 0 {
			notifyWatchlists(ctx, store, s, watchlistTargets)
		}

		if !touchesTenant {
			qr.skippedNonTenant++
			continue
		}

		var priority int
		switch {
		case !exists:
			priority = 1
		default:
			priority = 3
		}

		if err := store.EnqueueForScoring(ctx, s.Username, "github", priority); err != nil {
			slog.Debug("enqueue", "username", s.Username, "error", err)
			continue
		}
		qr.queued++
	}
	return qr
}

// notifyWatchlists writes notification events for any watchlist targets matching
// the contributor's repos. Only fires for new contributors (not yet scored).
func notifyWatchlists(ctx context.Context, store ingestStore, s Summary, targets map[string][]postgres.WatchlistEntry) {
	notified := make(map[string]bool) // dedupe by watchlist ID
	for repo := range s.Repos {
		owner := repo
		if idx := strings.Index(repo, "/"); idx > 0 {
			owner = repo[:idx]
		}

		// Match against org-level watchlists.
		for _, wl := range targets[owner] {
			if notified[wl.ID] {
				continue
			}
			if writeNewContributorEvent(ctx, store, wl, s) {
				notified[wl.ID] = true
			}
		}

		// Match against repo-level watchlists (org/repo).
		for _, wl := range targets[repo] {
			if notified[wl.ID] {
				continue
			}
			if writeNewContributorEvent(ctx, store, wl, s) {
				notified[wl.ID] = true
			}
		}
	}
}

// writeNewContributorEvent inserts a notification event filtered by plan scope.
func writeNewContributorEvent(ctx context.Context, store ingestStore, wl postgres.WatchlistEntry, s Summary) bool {
	details := buildEventDetails(wl.Plan, s)
	if details == nil {
		return false // no relevant activity for this plan scope
	}
	if err := store.InsertNotificationEvent(ctx, wl.ID, "new_contributor", s.Username, details); err != nil {
		slog.Debug("write notification event", "username", s.Username, "watchlist", wl.ID, "error", err)
		return false
	}
	return true
}

// buildEventDetails creates a details map filtered by the tenant's plan scope.
// Returns nil if the contributor has no relevant activity for the scope.
func buildEventDetails(planName string, s Summary) map[string]any {
	p, ok := plan.Get(planName)
	if !ok {
		p = plan.Free()
	}

	details := map[string]any{}
	hasActivity := false

	// All plans get PR data.
	if s.PRsOpened > 0 || s.PRsMerged > 0 || s.PRsClosed > 0 {
		details["prs_opened"] = s.PRsOpened
		details["prs_merged"] = s.PRsMerged
		details["prs_closed"] = s.PRsClosed
		hasActivity = true
	}

	// pr_review and pr_review_issue scopes include reviews.
	if p.WatchlistScope != "pr" && s.ReviewsGiven > 0 {
		details["reviews_given"] = s.ReviewsGiven
		hasActivity = true
	}

	// pr_review_issue scope includes issues and all comments.
	if p.WatchlistScope == "pr_review_issue" {
		if s.IssueComments > 0 {
			details["issue_comments"] = s.IssueComments
			hasActivity = true
		}
		if s.IssuesOpened > 0 {
			details["issues_opened"] = s.IssuesOpened
			hasActivity = true
		}
		if s.IssuesClosed > 0 {
			details["issues_closed"] = s.IssuesClosed
			hasActivity = true
		}
	}

	if !hasActivity {
		return nil
	}

	repos := make([]string, 0, len(s.Repos))
	for r := range s.Repos {
		repos = append(repos, r)
	}
	details["repos"] = repos
	return details
}

// maybeSendDigests sends weekly digest emails if enough time has passed.
func maybeSendDigests(ctx context.Context, store ingestStore) {
	lastSweep, err := store.GetSyncState(ctx, digestStateKey)
	if err != nil {
		slog.Warn("get digest state failed", "error", err)
	}
	if !lastSweep.IsZero() && time.Since(lastSweep) < digestInterval {
		return
	}

	apiKey := os.Getenv("SEND_API_KEY")
	baseURL := config.GetEnv("BASE_URL", "http://localhost:8080")
	if apiKey == "" {
		slog.Debug("digest email skipped, SEND_API_KEY not set")
		saveDigestState(ctx, store)
		return
	}

	dryRun := os.Getenv("DIGEST_DRY_RUN") != "false" // default ON (safe)
	adminSet := buildAdminSet()

	targets, err := store.GetTenantsWithUnsentEvents(ctx)
	if err != nil {
		slog.Error("get digest targets", "error", err)
		return
	}

	sent, skipped := sendDigests(ctx, store, targets, apiKey, baseURL, dryRun, adminSet)
	if sent > 0 || skipped > 0 {
		slog.Info("digest sweep complete", "sent", sent, "skipped", skipped)
	}

	saveDigestState(ctx, store)
}

func buildAdminSet() map[string]bool {
	adminUsers := strings.Split(os.Getenv("DEVTRACE_ADMIN_USERS"), ",")
	adminSet := make(map[string]bool, len(adminUsers))
	for _, u := range adminUsers {
		if u = strings.TrimSpace(u); u != "" {
			adminSet[u] = true
		}
	}
	return adminSet
}

// digestDay returns a deterministic day-of-week (0=Sunday..6=Saturday)
// for a tenant, distributing digest sends evenly across the week.
func digestDay(tenantID string) int {
	h := fnv.New32a()
	h.Write([]byte(tenantID))
	return int(h.Sum32() % 7)
}

func sendDigests(ctx context.Context, store ingestStore,
	targets []postgres.DigestTarget, apiKey, baseURL string,
	dryRun bool, adminSet map[string]bool) (sent, skipped int) {
	today := int(time.Now().UTC().Weekday())

	for _, t := range targets {
		p, ok := plan.Get(t.Plan)
		if !ok {
			p = plan.Free()
		}
		if !p.DigestEmail {
			continue
		}

		if dryRun && !adminSet[t.Username] {
			slog.Info("digest dry run, skipping", "tenant", t.Username)
			skipped++
			continue
		}

		// Each tenant is assigned a fixed day of the week; skip if not their day.
		if digestDay(t.TenantID) != today {
			continue
		}

		if sendOneDigest(ctx, store, t, apiKey, baseURL) {
			sent++
		}
	}
	return sent, skipped
}

func sendOneDigest(ctx context.Context, store ingestStore,
	t postgres.DigestTarget, apiKey, baseURL string) bool {
	events, err := store.GetUnsentEventsForDigest(ctx, t.TenantID, digestMaxEventsPerEmail)
	if err != nil {
		slog.Error("get digest events", "tenant", t.TenantID, "error", err)
		return false
	}
	if len(events) == 0 {
		return false
	}

	unsubURL := buildUnsubscribeURL(baseURL, t.TenantID)
	htmlBody, textBody := watchlist.RenderDigest(events, baseURL, unsubURL)
	if err := devnet.SendEmail(ctx, apiKey, "noreply@thingz.io", t.Email,
		"DevTrace Weekly Digest", htmlBody, textBody, "",
		devnet.WithUnsubscribeURL(unsubURL)); err != nil {
		slog.Error("send digest email", "tenant", t.Username, "error", err)
		return false
	}

	if err := store.MarkAllEventsSent(ctx, t.TenantID); err != nil {
		slog.Error("mark events sent", "tenant", t.TenantID, "error", err)
	}
	slog.Info("digest sent", "tenant", t.Username, "events", len(events))
	return true
}

func buildUnsubscribeURL(baseURL, tenantID string) string {
	secret := watchlist.HMACSecret()
	if secret == "" {
		return ""
	}
	token := watchlist.UnsubscribeToken(secret, tenantID)
	return fmt.Sprintf("%s/digest/unsubscribe?tenant=%s&token=%s", baseURL, tenantID, token)
}

func saveDigestState(ctx context.Context, store ingestStore) {
	if err := store.SaveSyncState(ctx, digestStateKey, time.Now().UTC()); err != nil {
		slog.Error("save digest state", "error", err)
	}
}
