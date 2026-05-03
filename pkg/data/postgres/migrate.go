package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

//go:embed sql/migrations/*.sql
var migrationsFS embed.FS

// migrateLockKey is the postgres advisory-lock key the Migrate routine
// holds for the duration of all schema applies. Concurrent boot from
// scale-to-zero can land two replicas in Migrate at the same instant; the
// lock serializes them so only one applies a given version. The number is
// arbitrary but stable — must not collide with other advisory-lock keys
// the application may use in the future.
const migrateLockKey int64 = 0x6465767472616365 // "devtrace" as ASCII bytes

func (s *Store) Migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("sql/migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	// Pin a single connection for the lifetime of the lock. Postgres
	// advisory locks are session-scoped, so we must use the same
	// connection for lock + migration applies + unlock. Without this,
	// the lock can land on connection A while the migration runs on
	// connection B — defeating the lock entirely.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrateLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// Best-effort: unlock errors are logged but do not mask the
		// caller's error. Closing the conn would also release the lock,
		// but explicit unlock is cleaner.
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrateLockKey); err != nil {
			slog.Warn("release migration lock", "error", err)
		}
	}()

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		version, err := strconv.Atoi(strings.Split(name, "_")[0])
		if err != nil {
			return fmt.Errorf("parse migration version %q: %w", name, err)
		}

		applied, err := migrationAppliedConn(ctx, conn, version)
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

		if _, err := conn.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("apply migration %q: %w", name, err)
		}

		if _, err := conn.ExecContext(ctx,
			"INSERT INTO devtrace_schema_version (version) VALUES ($1) ON CONFLICT DO NOTHING", version); err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}
	}

	return nil
}

func migrationAppliedConn(ctx context.Context, conn *sql.Conn, version int) (bool, error) {
	var exists bool
	err := conn.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'devtrace_schema_version')").Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check schema table: %w", err)
	}
	if !exists {
		return false, nil
	}

	var count int
	err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devtrace_schema_version WHERE version = $1", version).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check migration version: %w", err)
	}
	return count > 0, nil
}
