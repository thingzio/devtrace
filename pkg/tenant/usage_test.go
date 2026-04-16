package tenant

import (
	"context"
	"os"
	"testing"
	"time"

	// postgres driver
	_ "github.com/lib/pq"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

func TestRecordAndGetUsage(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	ctx := context.Background()
	store, err := postgres.NewFromEnv(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	if merr := store.Migrate(ctx); merr != nil {
		t.Fatalf("migrate: %v", merr)
	}

	db := store.DB()

	// Create a test tenant.
	tn, err := UpsertTenant(ctx, db, 999999, "quota-test-user", "qt@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert tenant: %v", err)
	}

	// Clean up any prior test data.
	_, _ = db.ExecContext(ctx, `DELETE FROM devtrace_usage_record WHERE tenant_id = $1`, tn.ID)

	// Record 3 events: 2 unique users.
	for _, u := range []string{"alice", "bob", "alice"} {
		if rerr := RecordUsage(ctx, db, tn.ID, u, "github", "ui", false, ""); rerr != nil {
			t.Fatalf("record usage for %s: %v", u, rerr)
		}
	}

	since := time.Now().UTC().Add(-1 * time.Minute)
	count, err := GetUsageCount(ctx, db, tn.ID, since)
	if err != nil {
		t.Fatalf("get usage count: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 unique users, got %d", count)
	}
}

func TestBillingPeriodStart(t *testing.T) {
	start := BillingPeriodStart()
	now := time.Now().UTC()

	if start.Year() != now.Year() || start.Month() != now.Month() || start.Day() != 1 {
		t.Errorf("expected first of current month, got %v", start)
	}
	if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 {
		t.Errorf("expected midnight UTC, got %v", start)
	}
}
