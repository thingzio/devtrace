package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thingzio/devtrace/pkg/model"
)

const (
	getPublisherPackagesSQL = `
		SELECT package_count, packages, fetched_at
		FROM devtrace_publisher_packages
		WHERE provider = $1 AND username = $2 AND registry = $3`

	upsertPublisherPackagesSQL = `
		INSERT INTO devtrace_publisher_packages
			(provider, username, registry, package_count, packages, fetched_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (provider, username, registry) DO UPDATE SET
			package_count = EXCLUDED.package_count,
			packages      = EXCLUDED.packages,
			fetched_at    = NOW()`
)

// GetPublisherProfile returns the cached publisher profile for the
// given user on the given registry. Returns (nil, zero-time, nil)
// when no row exists. The fetched_at timestamp lets callers decide
// whether to refresh against config.PublisherTTL.
//
// A zero-PackageCount profile is still returned (not collapsed to
// nil) so callers can distinguish "we looked, found nothing" from
// "never looked"; the UI render path treats zero-count as "no
// publisher data" but the service layer's TTL check uses the
// non-nil row to suppress repeated re-fetching.
func (s *Store) GetPublisherProfile(ctx context.Context, provider, username, registry string) (*model.RegistryProfile, time.Time, error) {
	var (
		count       int
		packagesRaw []byte
		fetchedAt   time.Time
	)
	err := s.db.QueryRowContext(ctx, getPublisherPackagesSQL,
		provider, username, registry).Scan(&count, &packagesRaw, &fetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("query publisher %s/%s: %w", registry, username, err)
	}

	out := &model.RegistryProfile{PackageCount: count}
	if len(packagesRaw) > 0 {
		if err := json.Unmarshal(packagesRaw, &out.Top); err != nil {
			return nil, time.Time{}, fmt.Errorf("unmarshal publisher packages: %w", err)
		}
	}
	return out, fetchedAt, nil
}

// SavePublisherProfile upserts the publisher row. Pass a non-nil
// profile with PackageCount=0 and empty Top to record a "fetched
// but no packages" sentinel — same TTL semantics as real data,
// suppressing re-fetching for users with no publisher account.
func (s *Store) SavePublisherProfile(ctx context.Context, provider, username, registry string, profile *model.RegistryProfile) error {
	if profile == nil {
		return errors.New("save publisher: nil profile")
	}
	packagesJSON, err := json.Marshal(profile.Top)
	if err != nil {
		return fmt.Errorf("marshal publisher packages: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, upsertPublisherPackagesSQL,
		provider, username, registry,
		profile.PackageCount, packagesJSON,
	); err != nil {
		return fmt.Errorf("upsert publisher %s/%s: %w", registry, username, err)
	}
	return nil
}
