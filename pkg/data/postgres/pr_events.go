package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PREventRow is the per-archive-hour observation written to the
// merge-graph table. Action is one of "opened", "merged", "closed".
// For the opened path Author is populated; for merged/closed it's
// empty (the actor is typically a CI bot we don't credit).
type PREventRow struct {
	Provider string
	Repo     string
	Number   int
	Action   string
	Author   string
	OccurAt  time.Time
}

const upsertPREventSQL = `
	INSERT INTO devtrace_pr_events
		(provider, repo, pr_number, author, opened_at, merged_at, closed_at, last_event_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
	ON CONFLICT (provider, repo, pr_number) DO UPDATE SET
		author        = COALESCE(EXCLUDED.author, devtrace_pr_events.author),
		opened_at     = COALESCE(EXCLUDED.opened_at, devtrace_pr_events.opened_at),
		merged_at     = COALESCE(EXCLUDED.merged_at, devtrace_pr_events.merged_at),
		closed_at     = COALESCE(EXCLUDED.closed_at, devtrace_pr_events.closed_at),
		last_event_at = NOW()`

// BatchUpsertPREvents writes per-PR observations into the merge graph.
// Each row stamps only the column corresponding to its action; the
// COALESCE-based UPDATE preserves earlier values so an opened event
// in March + a merged event in May land in the same row with both
// timestamps populated.
//
// Bot-opened PRs (Renovate, Dependabot, etc.) arrive with empty
// Author by aggregator-side filtering; their opened_at still gets
// stamped so we can recognize the PR existed, but they won't show
// up in author-attributed merge counts.
func (s *Store) BatchUpsertPREvents(ctx context.Context, events []PREventRow) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin pr events tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	count := 0
	for _, ev := range events {
		var (
			author   sql.NullString
			openedAt sql.NullTime
			mergedAt sql.NullTime
			closedAt sql.NullTime
		)
		if ev.Author != "" {
			author = sql.NullString{String: ev.Author, Valid: true}
		}
		t := sql.NullTime{Time: ev.OccurAt, Valid: !ev.OccurAt.IsZero()}
		switch ev.Action {
		case "opened":
			openedAt = t
		case "merged":
			mergedAt = t
		case "closed":
			closedAt = t
		default:
			// Unknown action; skip. Defensive — aggregator filters to
			// the three known actions before reaching this point.
			continue
		}

		if _, err := tx.ExecContext(ctx, upsertPREventSQL,
			ev.Provider, ev.Repo, ev.Number,
			author, openedAt, mergedAt, closedAt,
		); err != nil {
			return 0, fmt.Errorf("upsert pr event %s#%d: %w", ev.Repo, ev.Number, err)
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit pr events: %w", err)
	}
	return count, nil
}

// GetAuthoredMergedPRCount returns the number of PRs authored by the
// given user that have been merged. The query relies on the partial
// index idx_devtrace_pr_events_author_merged so it stays cheap even
// as the table grows.
//
// Returns 0 (not nil) for users with no observations, so callers
// don't need to special-case "never seen". An error is returned only
// on a real query failure.
func (s *Store) GetAuthoredMergedPRCount(ctx context.Context, provider, username string) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM devtrace_pr_events
		WHERE provider = $1 AND author = $2 AND merged_at IS NOT NULL`
	var n int
	if err := s.db.QueryRowContext(ctx, query, provider, username).Scan(&n); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("count authored merged prs %s: %w", username, err)
	}
	return n, nil
}

// PrunePREvents drops PR-event rows whose last_event_at is older
// than the retention window. Mirrors PruneActivity's cadence so the
// merge-graph table doesn't outgrow the activity history that scoring
// is anchored to.
func (s *Store) PrunePREvents(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM devtrace_pr_events WHERE last_event_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune pr events: %w", err)
	}
	return res.RowsAffected()
}

// PREventsStats holds operational metrics for the merge-graph table.
//
// IngestAttributionPct is the load-bearing pipeline-health number:
// of rows where we observed an opened event by a human (i.e. could
// have attributed authorship), what share did we actually attribute.
// Denominator = withAuthor + missingOpen — excludes bot-opened rows
// (intentional NULL author per aggregator policy) so it isolates true
// ingest gaps from structural NULLs. Near 100% on a healthy pipeline.
//
// AuthorAttributionPct is the overall withAuthor/total share. It
// conflates bot-opened and missing-open rows so a low value can mean
// "lots of Renovate churn" or "ingest dropping opens" — useful as a
// raw signal but not load-bearing. Prefer IngestAttributionPct.
//
// BotOpenedRows is rows where the opened event WAS observed but the
// opener was a bot (author stripped at aggregator). Renovate /
// Dependabot dominate this bucket.
//
// MissingOpenRows is rows with a merge or close event but no opened
// event observed. Driven by the backfill horizon: PRs opened before
// our earliest archive hour but merged inside it will permanently
// lack opened_at. Steady-state share, not a transient.
type PREventsStats struct {
	TotalRows            int
	Last24hRows          int
	AuthorAttributionPct float64
	BotOpenedRows        int
	MissingOpenRows      int
	IngestAttributionPct float64
	LastEventAt          time.Time
}

// PREventsStats returns aggregate state of the merge-graph table for
// the admin dashboard. All counts come from a single FILTER-based
// scan to keep the dashboard query cheap.
func (s *Store) PREventsStats(ctx context.Context) (*PREventsStats, error) {
	const query = `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE last_event_at > NOW() - INTERVAL '24 hours') AS last_24h,
			COUNT(*) FILTER (WHERE author IS NOT NULL) AS with_author,
			COUNT(*) FILTER (WHERE author IS NULL AND opened_at IS NOT NULL) AS bot_opened,
			COUNT(*) FILTER (WHERE author IS NULL AND opened_at IS NULL) AS missing_open,
			MAX(last_event_at) AS last_event_at
		FROM devtrace_pr_events`

	var (
		out         PREventsStats
		withAuthor  int
		lastEventAt sql.NullTime
	)
	if err := s.db.QueryRowContext(ctx, query).Scan(
		&out.TotalRows, &out.Last24hRows, &withAuthor,
		&out.BotOpenedRows, &out.MissingOpenRows, &lastEventAt,
	); err != nil {
		return nil, fmt.Errorf("pr events stats: %w", err)
	}
	if out.TotalRows > 0 {
		out.AuthorAttributionPct = float64(withAuthor) / float64(out.TotalRows) * 100
	}
	if denom := withAuthor + out.MissingOpenRows; denom > 0 {
		out.IngestAttributionPct = float64(withAuthor) / float64(denom) * 100
	}
	if lastEventAt.Valid {
		out.LastEventAt = lastEventAt.Time
	}
	return &out, nil
}
