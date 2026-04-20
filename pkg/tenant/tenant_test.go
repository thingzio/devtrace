package tenant_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

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

func TestSearchTenants(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Use unique GitHub IDs for this test.
	ids := []int64{99900010, 99900011, 99900012}
	t.Cleanup(func() {
		for _, id := range ids {
			cleanup(t, db, id)
		}
	})

	// Seed three tenants.
	for i, tc := range []struct {
		ghID     int64
		username string
		name     string
	}{
		{ids[0], "search-alpha", "Alpha User"},
		{ids[1], "search-beta", "Beta User"},
		{ids[2], "search-gamma", "Gamma Person"},
	} {
		_, err := tenant.UpsertTenant(ctx, db, tc.ghID, tc.username, tc.username+"@test.com", "", tc.name, "", "", "")
		if err != nil {
			t.Fatalf("upsert[%d]: %v", i, err)
		}
	}

	tests := []struct {
		name    string
		query   string
		limit   int
		offset  int
		wantMin int // at least this many results (other tenants may exist in DB)
		wantHit string
	}{
		{"empty query returns all", "", 100, 0, 3, "search-alpha"},
		{"search by username prefix", "search-beta", 100, 0, 1, "search-beta"},
		{"search by name", "Gamma", 100, 0, 1, "search-gamma"},
		{"case insensitive", "ALPHA", 100, 0, 1, "search-alpha"},
		{"no match", "zzz-nonexistent-999", 100, 0, 0, ""},
		{"pagination limit", "", 2, 0, 2, ""},
		{"pagination offset past results", "search-alpha", 100, 100, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, total, err := tenant.SearchTenants(ctx, db, tt.query, tt.limit, tt.offset)
			if err != nil {
				t.Fatalf("SearchTenants(%q): %v", tt.query, err)
			}
			if len(results) < tt.wantMin {
				t.Errorf("got %d results, want at least %d", len(results), tt.wantMin)
			}
			if tt.wantHit != "" {
				found := false
				for _, r := range results {
					if r.Username == tt.wantHit {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected %q in results", tt.wantHit)
				}
			}
			if tt.query == "" && tt.offset == 0 {
				if total < 3 {
					t.Errorf("total = %d, want at least 3", total)
				}
			}
			if tt.query == "zzz-nonexistent-999" {
				if total != 0 {
					t.Errorf("total = %d, want 0 for non-match", total)
				}
			}
		})
	}
}

func TestSearchTenants_PaginationTotal(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	ids := []int64{99900020, 99900021, 99900022}
	t.Cleanup(func() {
		for _, id := range ids {
			cleanup(t, db, id)
		}
	})

	for i, id := range ids {
		_, err := tenant.UpsertTenant(ctx, db, id, "pgtest-"+string(rune('a'+i)), "", "", "", "", "", "")
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// Fetch page 1 with limit=2, verify total reflects all matching.
	results, total, err := tenant.SearchTenants(ctx, db, "pgtest-", 2, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("page 1 results = %d, want 2", len(results))
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	// Fetch page 2.
	results2, total2, err := tenant.SearchTenants(ctx, db, "pgtest-", 2, 2)
	if err != nil {
		t.Fatalf("search page 2: %v", err)
	}
	if len(results2) != 1 {
		t.Errorf("page 2 results = %d, want 1", len(results2))
	}
	if total2 != 3 {
		t.Errorf("total page 2 = %d, want 3", total2)
	}
}

func TestSearchTenants_SortByLastSignIn(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	ids := []int64{99900030, 99900031, 99900032}
	t.Cleanup(func() {
		for _, id := range ids {
			cleanup(t, db, id)
		}
	})

	// Create three tenants.
	tn1, err := tenant.UpsertTenant(ctx, db, ids[0], "sorttest-old", "", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	tn2, err := tenant.UpsertTenant(ctx, db, ids[1], "sorttest-new", "", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	// tn3 has no session — should appear last.
	_, err = tenant.UpsertTenant(ctx, db, ids[2], "sorttest-none", "", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert 3: %v", err)
	}

	// Create session for tn1 first (older), then tn2 (newer).
	_, err = tenant.CreateSession(ctx, db, tn1.ID, 10*time.Minute)
	if err != nil {
		t.Fatalf("session 1: %v", err)
	}
	// Small delay to ensure distinct timestamps.
	time.Sleep(50 * time.Millisecond)
	_, err = tenant.CreateSession(ctx, db, tn2.ID, 10*time.Minute)
	if err != nil {
		t.Fatalf("session 2: %v", err)
	}

	results, _, err := tenant.SearchTenants(ctx, db, "sorttest-", 10, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	// Most recent sign-in (tn2) should be first, oldest (tn1) second, no session last.
	if results[0].Username != "sorttest-new" {
		t.Errorf("results[0] = %q, want sorttest-new (most recent sign-in)", results[0].Username)
	}
	if results[1].Username != "sorttest-old" {
		t.Errorf("results[1] = %q, want sorttest-old (older sign-in)", results[1].Username)
	}
	if results[2].Username != "sorttest-none" {
		t.Errorf("results[2] = %q, want sorttest-none (no sign-in)", results[2].Username)
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
