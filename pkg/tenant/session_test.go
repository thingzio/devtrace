package tenant_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestCreateAndValidateSession(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99910001

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "sess-alice", "sess-alice@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rawToken, err := tenant.CreateSession(ctx, db, created.ID, 10*time.Minute)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if rawToken == "" {
		t.Fatal("raw token is empty")
	}

	got, err := tenant.ValidateSession(ctx, db, rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("tenant id = %s, want %s", got.ID, created.ID)
	}
	if got.Username != "sess-alice" {
		t.Fatalf("username = %q, want %q", got.Username, "sess-alice")
	}
}

func TestValidateExpiredSession(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99910002

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "sess-bob", "sess-bob@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rawToken, err := tenant.CreateSession(ctx, db, created.ID, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	_, err = tenant.ValidateSession(ctx, db, rawToken)
	if !errors.Is(err, tenant.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
}

func TestDestroySession(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99910003

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "sess-carol", "sess-carol@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rawToken, err := tenant.CreateSession(ctx, db, created.ID, 10*time.Minute)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	err = tenant.DestroySession(ctx, db, rawToken)
	if err != nil {
		t.Fatalf("destroy session: %v", err)
	}

	_, err = tenant.ValidateSession(ctx, db, rawToken)
	if !errors.Is(err, tenant.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
}

func TestHashToken(t *testing.T) {
	const input = "test-token-value"
	want := sha256.Sum256([]byte(input))
	wantHex := hex.EncodeToString(want[:])

	got := tenant.HashToken(input)
	if got != wantHex {
		t.Fatalf("HashToken(%q) = %q, want %q", input, got, wantHex)
	}

	// Verify deterministic.
	if tenant.HashToken(input) != got {
		t.Fatal("HashToken is not deterministic")
	}
}
