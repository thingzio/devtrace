package postgres

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

//go:embed sql/migrations/*.sql
var migrationsFS embed.FS

func (s *Store) Migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("sql/migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		version, err := strconv.Atoi(strings.Split(name, "_")[0])
		if err != nil {
			return fmt.Errorf("parse migration version %q: %w", name, err)
		}

		applied, err := s.migrationApplied(ctx, version)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied {
			continue
		}

		data, err := migrationsFS.ReadFile("sql/migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", name, err)
		}

		slog.Info("applying migration", "version", version, "file", name)

		if _, err := s.db.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("apply migration %q: %w", name, err)
		}

		if _, err := s.db.ExecContext(ctx,
			"INSERT INTO devtrace_schema_version (version) VALUES ($1) ON CONFLICT DO NOTHING", version); err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}
	}

	return nil
}

func (s *Store) migrationApplied(ctx context.Context, version int) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'devtrace_schema_version')").Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check schema table: %w", err)
	}
	if !exists {
		return false, nil
	}

	var count int
	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devtrace_schema_version WHERE version = $1", version).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check migration version: %w", err)
	}
	return count > 0, nil
}
