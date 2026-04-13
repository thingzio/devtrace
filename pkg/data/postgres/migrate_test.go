package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

func testStore(t *testing.T) *postgres.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable"
	}
	store, err := postgres.New(context.Background(), dsn, postgres.DefaultPoolConfig())
	if err != nil {
		t.Skipf("skipping integration test: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestMigrate(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Run again to verify idempotency
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate (idempotent): %v", err)
	}
}
