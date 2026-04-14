package ingest

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
)

const syncStateKey = "gharchive_cursor"

// Run processes one or more hourly GH Archive dumps.
func Run(ctx context.Context, store *postgres.Store) error {
	baseURL := config.GetEnv("GHARCHIVE_BASE_URL", "")
	reader := NewArchiveReader(baseURL)

	cursor, _ := store.GetSyncState(ctx, syncStateKey)
	lookback := config.GetEnvAsInt("GHARCHIVE_LOOKBACK_HOURS", 1)
	hours := computeHours(cursor, lookback)

	if len(hours) == 0 {
		slog.Info("no hours to process")
		return nil
	}

	tenantRepos, _ := store.GetTenantRepos(ctx)
	slog.Info("ingest starting", "hours", len(hours), "tenant_orgs", len(tenantRepos))

	for _, hour := range hours {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := processHour(ctx, store, reader, hour, tenantRepos); err != nil {
			slog.Error("process hour failed", "hour", hour, "error", err)
			continue
		}
		if err := store.SaveSyncState(ctx, syncStateKey, hour); err != nil {
			slog.Error("save cursor", "error", err)
		}
	}

	return nil
}

// computeHours determines which hourly archive files to process.
// It returns up to lookback hours between cursor+1h and lastAvailable (now-1h).
func computeHours(cursor time.Time, lookback int) []time.Time {
	now := time.Now().UTC().Truncate(time.Hour)
	lastAvailable := now.Add(-time.Hour)

	var start time.Time
	if cursor.IsZero() {
		start = lastAvailable.Add(-time.Duration(lookback-1) * time.Hour)
	} else {
		start = cursor.Add(time.Hour)
	}

	if !start.Before(now) {
		return nil
	}

	var hours []time.Time
	for t := start; !t.After(lastAvailable) && len(hours) < lookback; t = t.Add(time.Hour) {
		hours = append(hours, t)
	}
	return hours
}

func processHour(ctx context.Context, store *postgres.Store, reader *ArchiveReader,
	hour time.Time, tenantRepos map[string]bool) error {
	slog.Info("processing archive", "hour", hour.Format("2006-01-02-15"))

	agg := NewAggregator(hour)
	var eventCount int

	err := reader.Stream(ctx, hour, func(ev Event) {
		agg.Add(ev)
		eventCount++
	})
	if err != nil {
		return err
	}

	results := agg.Results()
	slog.Info("aggregated", "events", eventCount, "contributors", len(results))

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
			DistinctRepos: len(repos),
			Repos:         repos,
		})
	}

	stored, err := store.BatchUpsertActivity(ctx, pgSummaries)
	if err != nil {
		return err
	}
	slog.Info("stored activity", "rows", stored)

	queued := queueContributors(ctx, store, results, tenantRepos)
	slog.Info("queued for scoring", "count", queued)

	return nil
}

func queueContributors(ctx context.Context, store *postgres.Store,
	summaries []Summary, tenantRepos map[string]bool) int {
	var count int
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

		var priority int
		switch {
		case !exists && touchesTenant:
			priority = 1
		case !exists:
			priority = 2
		case touchesTenant:
			priority = 3
		default:
			continue
		}

		if err := store.EnqueueForScoring(ctx, s.Username, "github", priority); err != nil {
			slog.Debug("enqueue", "username", s.Username, "error", err)
			continue
		}
		count++
	}
	return count
}
