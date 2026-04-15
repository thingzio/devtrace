package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DevPulseDeveloper represents a row from DevPulse's developer table.
type DevPulseDeveloper struct {
	Username          string
	FullName          string
	Email             string
	Avatar            string
	Reputation        float64
	ReputationDeep    bool
	ReputationSignals string // raw JSON from DevPulse
	UpdatedAt         time.Time
}

// GetDevPulseUpdatedDevelopers reads developers from DevPulse's developer table
// that have been updated since the given time. The developer table belongs to
// DevPulse and is read-only from DevTrace's perspective.
func (s *Store) GetDevPulseUpdatedDevelopers(ctx context.Context, since time.Time, limit int) ([]DevPulseDeveloper, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT username,
		        COALESCE(full_name,''),
		        COALESCE(email,''),
		        COALESCE(avatar,''),
		        COALESCE(reputation,0),
		        COALESCE(reputation_deep,0),
		        COALESCE(reputation_signals,'{}'),
		        COALESCE(reputation_updated_at,'')
		   FROM developer
		  WHERE reputation_updated_at > $1
		  ORDER BY reputation_updated_at ASC
		  LIMIT $2`,
		since.Format(time.RFC3339), limit)
	if err != nil {
		return nil, fmt.Errorf("query devpulse developers: %w", err)
	}
	defer rows.Close()

	var result []DevPulseDeveloper
	for rows.Next() {
		var d DevPulseDeveloper
		var deepInt int
		var updatedStr string
		if err := rows.Scan(&d.Username, &d.FullName, &d.Email, &d.Avatar,
			&d.Reputation, &deepInt, &d.ReputationSignals, &updatedStr); err != nil {
			return nil, fmt.Errorf("scan devpulse developer: %w", err)
		}
		d.ReputationDeep = deepInt != 0
		if updatedStr != "" {
			t, err := time.Parse(time.RFC3339, updatedStr)
			if err != nil {
				return nil, fmt.Errorf("parse reputation_updated_at %q: %w", updatedStr, err)
			}
			d.UpdatedAt = t
		}
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate devpulse developers: %w", err)
	}
	return result, nil
}

// SyncDeveloperToDevTrace upserts a DevPulse developer into DevTrace's
// contributor, reputation, and reputation_history tables. The grade parameter
// should be computed by the caller (e.g. via score.Grade). modelVersion
// identifies the scoring model that produced the reputation value.
func (s *Store) SyncDeveloperToDevTrace(ctx context.Context, d DevPulseDeveloper, grade, modelVersion string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort rollback on error path

	// Upsert contributor.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO devtrace_contributor (username, provider, display_name, email, avatar_url)
		 VALUES ($1, 'github', $2, $3, $4)
		 ON CONFLICT (username, provider) DO UPDATE
		    SET display_name = EXCLUDED.display_name,
		        email        = EXCLUDED.email,
		        avatar_url   = EXCLUDED.avatar_url,
		        updated_at   = NOW()`,
		d.Username, d.FullName, d.Email, d.Avatar); err != nil {
		return fmt.Errorf("upsert contributor: %w", err)
	}

	// Upsert reputation.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO devtrace_reputation (username, provider, score, grade, model_version, deep, signals)
		 VALUES ($1, 'github', $2, $3, $4, $5, $6::jsonb)
		 ON CONFLICT (username, provider) DO UPDATE
		    SET score         = EXCLUDED.score,
		        grade         = EXCLUDED.grade,
		        model_version = EXCLUDED.model_version,
		        deep          = EXCLUDED.deep,
		        signals       = EXCLUDED.signals,
		        scored_at     = NOW()`,
		d.Username, d.Reputation, grade, modelVersion, d.ReputationDeep, d.ReputationSignals); err != nil {
		return fmt.Errorf("upsert reputation: %w", err)
	}

	// Append to reputation_history.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO devtrace_reputation_history (username, provider, score, grade, deep)
		 VALUES ($1, 'github', $2, $3, $4)`,
		d.Username, d.Reputation, grade, d.ReputationDeep); err != nil {
		return fmt.Errorf("insert reputation_history: %w", err)
	}

	return tx.Commit()
}

// GetSyncState returns the time stored under the given key in sync_state.
// Returns the zero time if the key does not exist.
func (s *Store) GetSyncState(ctx context.Context, key string) (time.Time, error) {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM devtrace_sync_state WHERE key = $1`, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("get sync state %q: %w", key, err)
	}

	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse sync state %q: %w", val, err)
	}
	return t, nil
}

// SaveSyncState upserts the given time value under key in sync_state.
func (s *Store) SaveSyncState(ctx context.Context, key string, val time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devtrace_sync_state (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, val.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save sync state %q: %w", key, err)
	}
	return nil
}
