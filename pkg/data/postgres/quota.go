package postgres

import (
	"context"
	"fmt"
	"time"
)

// TokenQuotaSample is a single rate-limit snapshot for one installation.
// Core REST, Search, and GraphQL families are sampled together so the
// admin chart can show which family is closest to its ceiling. Pre-cutover
// rows have search/graphql limits of 0; consumers treat a zero limit as
// "no data" for that family rather than 100% utilization.
type TokenQuotaSample struct {
	SampledAt      time.Time `json:"sampled_at"`
	InstallationID int64     `json:"installation_id"`
	Login          string    `json:"login"`
	QuotaLimit     int       `json:"quota_limit"`
	QuotaUsed      int       `json:"quota_used"`
	SearchLimit    int       `json:"search_limit"`
	SearchUsed     int       `json:"search_used"`
	GraphQLLimit   int       `json:"graphql_limit"`
	GraphQLUsed    int       `json:"graphql_used"`
}

//nolint:gosec // SQL constant, not a credential
const recordTokenQuotaSampleSQL = `
	INSERT INTO devtrace_token_quota_sample (
		installation_id, login,
		quota_limit, quota_used,
		search_limit, search_used,
		graphql_limit, graphql_used
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// RecordTokenQuotaSample writes a single quota snapshot to the time-series
// table. Pass per-family (limit, used) pairs for core (REST), search, and
// graphql. Callers that only know about core can pass 0 for the others;
// existing pre-cutover rows behave the same way.
func (s *Store) RecordTokenQuotaSample(ctx context.Context, installationID int64, login string,
	coreLimit, coreUsed, searchLimit, searchUsed, graphqlLimit, graphqlUsed int) error {
	if _, err := s.db.ExecContext(ctx, recordTokenQuotaSampleSQL,
		installationID, login,
		coreLimit, coreUsed,
		searchLimit, searchUsed,
		graphqlLimit, graphqlUsed,
	); err != nil {
		return fmt.Errorf("recording token quota sample: %w", err)
	}
	return nil
}

const getTokenQuotaSamplesSQL = `
	SELECT sampled_at, installation_id, login,
	       quota_limit, quota_used,
	       search_limit, search_used,
	       graphql_limit, graphql_used
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
		if err := rows.Scan(
			&sample.SampledAt, &sample.InstallationID, &sample.Login,
			&sample.QuotaLimit, &sample.QuotaUsed,
			&sample.SearchLimit, &sample.SearchUsed,
			&sample.GraphQLLimit, &sample.GraphQLUsed,
		); err != nil {
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
