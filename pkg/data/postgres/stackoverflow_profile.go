package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/thingzio/devtrace/pkg/model"
)

const (
	getStackOverflowSQL = `
		SELECT so_user_id, COALESCE(display_name, ''), reputation,
		       badge_bronze, badge_silver, badge_gold,
		       COALESCE(url, ''), so_created_at, last_access_at, fetched_at
		FROM devtrace_stackoverflow_profile
		WHERE provider = $1 AND username = $2`

	upsertStackOverflowSQL = `
		INSERT INTO devtrace_stackoverflow_profile
			(provider, username, so_user_id, display_name, reputation,
			 badge_bronze, badge_silver, badge_gold, url,
			 so_created_at, last_access_at, fetched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		ON CONFLICT (provider, username) DO UPDATE SET
			so_user_id     = EXCLUDED.so_user_id,
			display_name   = EXCLUDED.display_name,
			reputation     = EXCLUDED.reputation,
			badge_bronze   = EXCLUDED.badge_bronze,
			badge_silver   = EXCLUDED.badge_silver,
			badge_gold     = EXCLUDED.badge_gold,
			url            = EXCLUDED.url,
			so_created_at  = EXCLUDED.so_created_at,
			last_access_at = EXCLUDED.last_access_at,
			fetched_at     = NOW()`
)

// GetStackOverflowProfile returns the cached SO profile for a user.
// Returns (nil, zero-time, nil) when no row exists.
//
// A zero-rep / zero-user-id row is the sentinel for "we looked, no
// SO link declared" (or "the link pointed to a missing user"); the
// row keeps a non-zero fetched_at so the service-layer TTL check
// suppresses re-asking. The UI render path treats user_id==0 as
// "no SO data" and omits the section.
func (s *Store) GetStackOverflowProfile(ctx context.Context, provider, username string) (*model.StackOverflow, time.Time, error) {
	var (
		out          model.StackOverflow
		createdAt    sql.NullTime
		lastAccessAt sql.NullTime
		fetchedAt    time.Time
	)
	err := s.db.QueryRowContext(ctx, getStackOverflowSQL, provider, username).Scan(
		&out.UserID, &out.DisplayName, &out.Reputation,
		&out.BadgeBronze, &out.BadgeSilver, &out.BadgeGold,
		&out.URL, &createdAt, &lastAccessAt, &fetchedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("query stackoverflow %s: %w", username, err)
	}
	if createdAt.Valid {
		t := createdAt.Time
		out.CreatedAt = &t
	}
	if lastAccessAt.Valid {
		t := lastAccessAt.Time
		out.LastAccessAt = &t
	}
	return &out, fetchedAt, nil
}

// SaveStackOverflowProfile upserts the SO profile row. Pass a non-nil
// profile with UserID=0 and Reputation=0 to record a "no SO link
// declared" sentinel; the row keeps fetched_at fresh so the service
// layer's TTL check suppresses repeated re-checks.
func (s *Store) SaveStackOverflowProfile(ctx context.Context, provider, username string, profile *model.StackOverflow) error {
	if profile == nil {
		return errors.New("save stackoverflow: nil profile")
	}
	var createdAt, lastAccessAt sql.NullTime
	if profile.CreatedAt != nil && !profile.CreatedAt.IsZero() {
		createdAt = sql.NullTime{Time: *profile.CreatedAt, Valid: true}
	}
	if profile.LastAccessAt != nil && !profile.LastAccessAt.IsZero() {
		lastAccessAt = sql.NullTime{Time: *profile.LastAccessAt, Valid: true}
	}
	if _, err := s.db.ExecContext(ctx, upsertStackOverflowSQL,
		provider, username,
		profile.UserID, nullableString(profile.DisplayName), profile.Reputation,
		profile.BadgeBronze, profile.BadgeSilver, profile.BadgeGold,
		nullableString(profile.URL), createdAt, lastAccessAt,
	); err != nil {
		return fmt.Errorf("upsert stackoverflow %s: %w", username, err)
	}
	return nil
}
