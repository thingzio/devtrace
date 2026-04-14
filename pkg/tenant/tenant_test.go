package tenant_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable"
	}
	store, err := postgres.New(context.Background(), dsn, postgres.DefaultPoolConfig())
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store.DB()
}

func cleanup(t *testing.T, db *sql.DB, githubID int64) {
	t.Helper()
	_, _ = db.ExecContext(context.Background(), "DELETE FROM devtrace_tenant WHERE github_id = $1", githubID)
}

func TestUpsertTenant(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99900001

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	// Create new tenant.
	got, err := tenant.UpsertTenant(ctx, db, ghID, "alice", "alice@test.com", "https://avatar/alice", "Alice", "Acme", "NYC", "dev")
	if err != nil {
		t.Fatalf("upsert (create): %v", err)
	}
	if got.GitHubID != ghID {
		t.Fatalf("github_id = %d, want %d", got.GitHubID, ghID)
	}
	if got.Username != "alice" {
		t.Fatalf("username = %q, want %q", got.Username, "alice")
	}
	if got.Plan != "free" {
		t.Fatalf("plan = %q, want %q", got.Plan, "free")
	}
	if got.MaxContributors != 50 {
		t.Fatalf("max_contributors = %d, want 50", got.MaxContributors)
	}
	if got.ToSAcceptedAt != nil {
		t.Fatal("tos_accepted_at should be nil on create")
	}

	// Upsert same github_id with new username.
	updated, err := tenant.UpsertTenant(ctx, db, ghID, "alice-new", "alice2@test.com", "https://avatar/alice2", "Alice N", "Acme2", "SF", "eng")
	if err != nil {
		t.Fatalf("upsert (update): %v", err)
	}
	if updated.ID != got.ID {
		t.Fatalf("id changed after upsert: %s != %s", updated.ID, got.ID)
	}
	if updated.Username != "alice-new" {
		t.Fatalf("username after upsert = %q, want %q", updated.Username, "alice-new")
	}
}

func TestGetTenantByID(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99900002

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "bob", "bob@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := tenant.GetTenantByID(ctx, db, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.GitHubID != ghID {
		t.Fatalf("github_id = %d, want %d", got.GitHubID, ghID)
	}
	if got.Username != "bob" {
		t.Fatalf("username = %q, want %q", got.Username, "bob")
	}
}

func TestGetTenantByGitHubID(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99900003

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "carol", "carol@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := tenant.GetTenantByGitHubID(ctx, db, ghID)
	if err != nil {
		t.Fatalf("get by github_id: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("id = %s, want %s", got.ID, created.ID)
	}
}

func TestAcceptToS(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99900004

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "dave", "dave@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if created.ToSAcceptedAt != nil {
		t.Fatal("tos_accepted_at should be nil before accept")
	}

	err = tenant.AcceptToS(ctx, db, created.ID)
	if err != nil {
		t.Fatalf("accept tos: %v", err)
	}

	got, err := tenant.GetTenantByID(ctx, db, created.ID)
	if err != nil {
		t.Fatalf("get after tos: %v", err)
	}
	if got.ToSAcceptedAt == nil {
		t.Fatal("tos_accepted_at should be set after accept")
	}
}
