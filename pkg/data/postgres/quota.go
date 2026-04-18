package postgres

import (
	"context"
	"fmt"
	"time"
)

// TokenQuotaSample is a single rate-limit snapshot for one installation.
type TokenQuotaSample struct {
	SampledAt      time.Time `json:"sampled_at"`
	InstallationID int64     `json:"installation_id"`
	Login          string    `json:"login"`
	QuotaLimit     int       `json:"quota_limit"`
	QuotaUsed      int       `json:"quota_used"`
}

//nolint:gosec // SQL constant, not a credential
const recordTokenQuotaSampleSQL = `
	INSERT INTO devtrace_token_quota_sample (installation_id, login, quota_limit, quota_used)
	VALUES ($1, $2, $3, $4)`

// RecordTokenQuotaSample writes a single quota snapshot to the time-series table.
func (s *Store) RecordTokenQuotaSample(ctx context.Context, installationID int64, login string, limit, used int) error {
	if _, err := s.db.ExecContext(ctx, recordTokenQuotaSampleSQL, installationID, login, limit, used); err != nil {
		return fmt.Errorf("recording token quota sample: %w", err)
	}
	return nil
}

const getTokenQuotaSamplesSQL = `
	SELECT sampled_at, installation_id, login, quota_limit, quota_used
	FROM devtrace_token_quota_sample
	WHERE sampled_at >= $1
	ORDER BY sampled_at ASC`

// GetTokenQuotaSamples returns all quota samples since the given time.
func (s *Store) GetTokenQuotaSamples(ctx context.Context, since time.Time) ([]TokenQuotaSample, error) {
	rows, err := s.db.QueryContext(ctx, getTokenQuotaSamplesSQL, since)
	if err != nil {
		return nil, fmt.Errorf("querying token quota samples: %w", err)
	}
	defer rows.Close()

	var out []TokenQuotaSample
	for rows.Next() {
		var sample TokenQuotaSample
		if err := rows.Scan(&sample.SampledAt, &sample.InstallationID, &sample.Login, &sample.QuotaLimit, &sample.QuotaUsed); err != nil {
			return nil, fmt.Errorf("scanning token quota sample: %w", err)
		}
		out = append(out, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating token quota samples: %w", err)
	}
	return out, nil
}

//nolint:gosec // SQL constant, not a credential
const purgeTokenQuotaSamplesSQL = `
	DELETE FROM devtrace_token_quota_sample WHERE sampled_at < $1`

// PurgeTokenQuotaSamples deletes samples older than the given time.
func (s *Store) PurgeTokenQuotaSamples(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, purgeTokenQuotaSamplesSQL, before)
	if err != nil {
		return 0, fmt.Errorf("purging old token quota samples: %w", err)
	}
	return res.RowsAffected()
}
